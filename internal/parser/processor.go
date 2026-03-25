package parser

import (
	"encoding/json"
	"fmt"
	"path/filepath"
	"strings"
)

// ProcessSession reads a session JSONL file and produces a ProcessedSession blob.
func ProcessSession(jsonlPath string) (*ProcessedSession, error) {
	entries, err := ReadJSONL(jsonlPath)
	if err != nil {
		return nil, err
	}

	if len(entries) == 0 {
		return nil, fmt.Errorf("empty session: %s", jsonlPath)
	}

	// Extract session metadata from the first entry
	meta := extractMetadata(entries)

	// Process entries into messages
	sessionDir := strings.TrimSuffix(jsonlPath, ".jsonl")
	messages, err := processEntries(entries, sessionDir)
	if err != nil {
		return nil, err
	}

	// Count user messages (for message_count — only real user inputs)
	userCount := 0
	for _, m := range messages {
		if m.Type == "user" {
			userCount++
		}
	}

	// Redact sensitive tool calls (.env reads, etc.)
	redactSensitiveBlocks(messages)

	meta.MessageCount = len(messages)

	return &ProcessedSession{
		Version:  1,
		Session:  meta,
		Messages: messages,
	}, nil
}

func redactSensitiveBlocks(messages []Message) {
	for i := range messages {
		for j := range messages[i].Blocks {
			block := &messages[i].Blocks[j]
			if block.Type != "tool_call" {
				continue
			}
			inputMap, ok := block.Input.(map[string]any)
			if !ok {
				continue
			}
			if IsSensitiveToolCall(block.Tool, inputMap) {
				RedactToolCall(block)
			}
			// Also redact inside subagent messages
			if block.Subagent != nil {
				redactSensitiveBlocks(block.Subagent.Messages)
			}
		}
	}
}

func extractMetadata(entries []RawEntry) SessionMetadata {
	// Use first entry for session-level metadata
	first := entries[0]
	meta := SessionMetadata{
		ID:        first.SessionID,
		CWD:       SanitizeString(first.CWD),
		GitBranch: first.GitBranch,
		Version:   first.Version,
		StartedAt: first.Timestamp,
	}

	// Derive project from CWD
	if first.CWD != "" {
		meta.Project = SanitizeString(first.CWD)
	}

	return meta
}

func processEntries(entries []RawEntry, sessionDir string) ([]Message, error) {
	var messages []Message
	var currentBlocks []Block
	var currentTimestamp string
	var currentID string
	// Track pending tool_calls so we can merge tool_results into them
	pendingTools := make(map[string]int) // tool_use_id → index in currentBlocks

	flushAssistant := func() {
		if len(currentBlocks) == 0 {
			return
		}
		messages = append(messages, Message{
			ID:        currentID,
			Type:      "assistant",
			Timestamp: currentTimestamp,
			Blocks:    currentBlocks,
		})
		currentBlocks = nil
		currentTimestamp = ""
		currentID = ""
		pendingTools = make(map[string]int)
	}

	for _, entry := range entries {
		if entry.Message == nil {
			continue
		}

		msg, err := ParseMessage(entry.Message)
		if err != nil {
			continue
		}

		switch msg.Role {
		case "user":
			if IsStringContent(msg.Content) {
				// Real user input — flush pending assistant turn
				flushAssistant()

				text, err := ParseStringContent(msg.Content)
				if err != nil {
					continue
				}
				messages = append(messages, Message{
					ID:        entry.UUID,
					Type:      "user",
					Timestamp: entry.Timestamp,
					Blocks: []Block{
						{Type: "text", Text: text},
					},
				})
			} else {
				// tool_result blocks — merge into pending assistant tool_calls
				blocks, err := ParseContentBlocks(msg.Content)
				if err != nil {
					continue
				}
				for _, block := range blocks {
					if block.Type != "tool_result" {
						continue
					}
					idx, ok := pendingTools[block.ToolUseID]
					if !ok {
						continue
					}

					output := ExtractToolResultOutput(block.Content)
					output, truncated, originalBytes := TruncateResult(output)

					currentBlocks[idx].Result = &ToolResult{
						Output:        output,
						Truncated:     truncated,
						OriginalBytes: originalBytes,
					}

					// Handle Agent tool — inline subagent
					if currentBlocks[idx].Tool == "Agent" {
						agentID := ExtractAgentID(output)
						if agentID != "" {
							subagentPath := filepath.Join(sessionDir, "subagents", "agent-"+agentID+".jsonl")
							subMessages, err := processSubagent(subagentPath)
							if err == nil && len(subMessages) > 0 {
								currentBlocks[idx].Subagent = &SubagentData{
									ID:       agentID,
									Messages: subMessages,
								}
							}
						}
					}
				}
			}

		case "assistant":
			blocks, err := ParseContentBlocks(msg.Content)
			if err != nil || len(blocks) == 0 {
				continue
			}

			block := blocks[0] // always exactly 1 block per JSONL line

			if currentTimestamp == "" {
				currentTimestamp = entry.Timestamp
				currentID = entry.UUID
			}

			switch block.Type {
			case "thinking":
				if block.Thinking != "" {
					currentBlocks = append(currentBlocks, Block{
						Type: "thinking",
						Text: block.Thinking,
					})
				}

			case "text":
				if block.Text != "" {
					currentBlocks = append(currentBlocks, Block{
						Type: "text",
						Text: block.Text,
					})
				}

			case "tool_use":
				// Parse input into a generic map for sanitization
				var inputRaw any
				if block.Input != nil {
					sanitized := SanitizePaths(block.Input)
					json.Unmarshal(sanitized, &inputRaw)
				}

				idx := len(currentBlocks)
				currentBlocks = append(currentBlocks, Block{
					Type:  "tool_call",
					Tool:  block.Name,
					Input: inputRaw,
				})
				pendingTools[block.ID] = idx
			}
		}
	}

	// Flush final assistant turn
	flushAssistant()

	return messages, nil
}

func processSubagent(path string) ([]Message, error) {
	entries, err := ReadJSONL(path)
	if err != nil {
		return nil, err
	}

	// Subagents don't have nested subagents in practice,
	// but use the same sessionDir logic just in case
	sessionDir := strings.TrimSuffix(path, ".jsonl")
	return processEntries(entries, sessionDir)
}
