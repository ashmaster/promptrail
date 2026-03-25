package parser

import (
	"encoding/json"
	"fmt"
	"os"
	"strings"
)

const maxResultBytes = 10 * 1024 // 10KB
const headBytes = 5 * 1024
const tailBytes = 5 * 1024

// TruncateResult truncates tool result output if it exceeds maxResultBytes.
func TruncateResult(output string) (string, bool, int) {
	if len(output) <= maxResultBytes {
		return output, false, 0
	}
	original := len(output)
	head := output[:headBytes]
	tail := output[len(output)-tailBytes:]
	truncated := head + "\n...[truncated]...\n" + tail
	return truncated, true, original
}

// SanitizePaths replaces the home directory with ~/ in the given JSON value.
func SanitizePaths(input json.RawMessage) json.RawMessage {
	home := homeDir()
	if home == "" {
		return input
	}
	s := string(input)
	s = strings.ReplaceAll(s, jsonEscape(home+"/"), "~/")
	s = strings.ReplaceAll(s, jsonEscape(home), "~")
	return json.RawMessage(s)
}

// SanitizeString replaces the home directory with ~/ in a plain string.
func SanitizeString(s string) string {
	home := homeDir()
	if home == "" {
		return s
	}
	s = strings.ReplaceAll(s, home+"/", "~/")
	s = strings.ReplaceAll(s, home, "~")
	return s
}

func homeDir() string {
	h, _ := os.UserHomeDir()
	return h
}

// jsonEscape escapes a string for safe replacement inside JSON values.
// Handles the fact that paths in JSON have / as-is but \ needs escaping.
func jsonEscape(s string) string {
	b, err := json.Marshal(s)
	if err != nil {
		return s
	}
	// Strip surrounding quotes
	return string(b[1 : len(b)-1])
}

const redactedMsg = "[redacted: .env file contents hidden for security]"

// sensitiveFilePatterns are file name patterns whose contents should be redacted.
var sensitiveFilePatterns = []string{
	".env",
	".env.local",
	".env.production",
	".env.development",
	".env.staging",
}

// sensitiveCommandPatterns are Bash substrings that suggest reading sensitive files.
var sensitiveCommandPatterns = []string{
	".env",
}

// IsSensitiveToolCall checks if a tool call involves reading/writing sensitive files
// (like .env) and should have its result and input content redacted.
func IsSensitiveToolCall(tool string, input map[string]any) bool {
	switch tool {
	case "Read", "Write", "Edit":
		fp, _ := input["file_path"].(string)
		return isSensitiveFilePath(fp)
	case "Bash":
		cmd, _ := input["command"].(string)
		return isSensitiveBashCommand(cmd)
	}
	return false
}

// RedactToolCall returns a redacted copy of the tool call block.
func RedactToolCall(block *Block) {
	if block.Result != nil {
		block.Result = &ToolResult{
			Output:    redactedMsg,
			Truncated: false,
		}
	}

	// Also redact Write content and Edit old_string/new_string from input
	if m, ok := block.Input.(map[string]any); ok {
		tool := block.Tool
		if tool == "Write" {
			if _, has := m["content"]; has {
				m["content"] = redactedMsg
			}
		}
		if tool == "Edit" {
			if _, has := m["old_string"]; has {
				m["old_string"] = redactedMsg
			}
			if _, has := m["new_string"]; has {
				m["new_string"] = redactedMsg
			}
		}
	}
}

func isSensitiveFilePath(fp string) bool {
	if fp == "" {
		return false
	}
	// Get the base file name
	parts := strings.Split(fp, "/")
	baseName := parts[len(parts)-1]

	for _, pattern := range sensitiveFilePatterns {
		// Exact match or starts with pattern (e.g., .env.local, .env.production)
		if baseName == pattern || strings.HasPrefix(baseName, ".env.") || baseName == ".env" {
			return true
		}
		_ = pattern
	}
	return false
}

func isSensitiveBashCommand(cmd string) bool {
	if cmd == "" {
		return false
	}
	// Check if the command reads a .env file
	// Common patterns: cat .env, head .env, less .env, source .env, etc.
	cmdLower := strings.ToLower(cmd)
	for _, pattern := range sensitiveCommandPatterns {
		if strings.Contains(cmdLower, pattern) {
			// Further check: is it actually accessing the file (not just mentioning .env in a grep pattern or echo)?
			// Look for read-like commands + .env as argument
			readCmds := []string{"cat ", "head ", "tail ", "less ", "more ", "bat ", "source ", ". ", "nano ", "vim ", "vi "}
			for _, rc := range readCmds {
				if strings.Contains(cmdLower, rc) && strings.Contains(cmd, pattern) {
					return true
				}
			}
			// Also catch: cat < .env, or just plain "cat .env"
			if strings.Contains(cmd, "< .env") || strings.Contains(cmd, "<.env") {
				return true
			}
		}
	}
	return false
}

// ExtractAgentID parses the agent ID from a tool result that contains
// "agentId: <id>" in the metadata footer.
func ExtractAgentID(result string) string {
	// Agent results have a footer like:
	// agentId: abb26ff58f06cc03e (use SendMessage ...)
	lines := strings.Split(result, "\n")
	for _, line := range lines {
		line = strings.TrimSpace(line)
		if strings.HasPrefix(line, "agentId: ") {
			parts := strings.Fields(line)
			if len(parts) >= 2 {
				return parts[1]
			}
		}
	}
	return ""
}

// ExtractToolResultOutput extracts a plain string from tool_result content.
// Content can be a string or a list of {type: "text", text: "..."} blocks.
func ExtractToolResultOutput(raw json.RawMessage) string {
	if len(raw) == 0 {
		return ""
	}

	// Try string first
	var s string
	if err := json.Unmarshal(raw, &s); err == nil {
		return s
	}

	// Try list of blocks
	var blocks []struct {
		Type string `json:"type"`
		Text string `json:"text"`
	}
	if err := json.Unmarshal(raw, &blocks); err == nil {
		var parts []string
		for _, b := range blocks {
			if b.Type == "tool_reference" {
				continue // skip tool loading markers
			}
			if b.Text != "" {
				parts = append(parts, b.Text)
			}
		}
		return strings.Join(parts, "\n")
	}

	return fmt.Sprintf("%s", raw)
}
