package parser

import (
	"bufio"
	"encoding/json"
	"fmt"
	"os"
)

// ReadJSONL reads a JSONL file and returns filtered entries (only user and assistant).
func ReadJSONL(path string) ([]RawEntry, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, fmt.Errorf("open %s: %w", path, err)
	}
	defer f.Close()

	var entries []RawEntry
	scanner := bufio.NewScanner(f)
	scanner.Buffer(make([]byte, 0, 1024*1024), 10*1024*1024) // 10MB max line

	for scanner.Scan() {
		line := scanner.Bytes()
		if len(line) == 0 {
			continue
		}

		var entry RawEntry
		if err := json.Unmarshal(line, &entry); err != nil {
			continue // skip malformed lines
		}

		// Only keep user and assistant messages
		if entry.Type == "user" || entry.Type == "assistant" {
			entries = append(entries, entry)
		}
	}

	if err := scanner.Err(); err != nil {
		return nil, fmt.Errorf("scan %s: %w", path, err)
	}

	return entries, nil
}

// ParseMessage parses the raw message JSON into role + content.
func ParseMessage(raw json.RawMessage) (*RawMessage, error) {
	var msg RawMessage
	if err := json.Unmarshal(raw, &msg); err != nil {
		return nil, err
	}
	return &msg, nil
}

// ParseContentBlocks parses content as an array of ContentBlock.
func ParseContentBlocks(raw json.RawMessage) ([]ContentBlock, error) {
	var blocks []ContentBlock
	if err := json.Unmarshal(raw, &blocks); err != nil {
		return nil, err
	}
	return blocks, nil
}

// IsStringContent checks if the content is a plain string (real user input).
func IsStringContent(raw json.RawMessage) bool {
	if len(raw) == 0 {
		return false
	}
	return raw[0] == '"'
}

// ParseStringContent extracts a plain string from content.
func ParseStringContent(raw json.RawMessage) (string, error) {
	var s string
	if err := json.Unmarshal(raw, &s); err != nil {
		return "", err
	}
	return s, nil
}
