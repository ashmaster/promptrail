package parser

import "encoding/json"

// ProcessedSession is the final blob format stored in R2 and consumed by renderers.
type ProcessedSession struct {
	Version  int             `json:"version"`
	Session  SessionMetadata `json:"session"`
	Messages []Message       `json:"messages"`
}

type SessionMetadata struct {
	ID           string `json:"id"`
	Project      string `json:"project"`
	CWD          string `json:"cwd"`
	GitBranch    string `json:"git_branch"`
	Version      string `json:"claude_version"`
	StartedAt    string `json:"started_at"`
	MessageCount int    `json:"message_count"`
}

type Message struct {
	ID        string  `json:"id"`
	Type      string  `json:"type"` // "user" or "assistant"
	Timestamp string  `json:"timestamp"`
	Blocks    []Block `json:"blocks"`
}

type Block struct {
	Type     string       `json:"type"`               // "text", "thinking", "tool_call"
	Text     string       `json:"text,omitempty"`      // for text and thinking
	Tool     string       `json:"tool,omitempty"`      // for tool_call
	Input    any          `json:"input,omitempty"`      // for tool_call
	Result   *ToolResult  `json:"result,omitempty"`     // for tool_call
	Subagent *SubagentData `json:"subagent,omitempty"` // for Agent tool_call only
}

type ToolResult struct {
	Output        string `json:"output"`
	Truncated     bool   `json:"truncated"`
	OriginalBytes int    `json:"original_bytes,omitempty"`
}

type SubagentData struct {
	ID       string    `json:"id"`
	Messages []Message `json:"messages"`
}

// Raw JSONL types (for parsing)

type RawEntry struct {
	Type      string          `json:"type"`
	Message   json.RawMessage `json:"message,omitempty"`
	UUID      string          `json:"uuid"`
	Timestamp string          `json:"timestamp"`
	SessionID string          `json:"sessionId"`
	CWD       string          `json:"cwd"`
	Version   string          `json:"version"`
	GitBranch string          `json:"gitBranch"`
	AgentID   string          `json:"agentId"`
}

type RawMessage struct {
	Role    string            `json:"role"`
	Content json.RawMessage   `json:"content"`
}

type ContentBlock struct {
	Type      string          `json:"type"`
	Text      string          `json:"text,omitempty"`
	Thinking  string          `json:"thinking,omitempty"`
	ID        string          `json:"id,omitempty"`        // tool_use id
	Name      string          `json:"name,omitempty"`      // tool_use name
	Input     json.RawMessage `json:"input,omitempty"`     // tool_use input
	ToolUseID string          `json:"tool_use_id,omitempty"` // tool_result
	Content   json.RawMessage `json:"content,omitempty"`    // tool_result content (reuse field name)
}

// toolUseID → Block index tracking for merging tool results
type pendingToolCall struct {
	blockIndex int
	toolUseID  string
}
