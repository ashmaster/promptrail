package parser

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

type LocalSession struct {
	ID           string
	ShortID      string
	ProjectDir   string // e.g., -Users-ashmaster-personal-whiteboard
	ProjectPath  string // e.g., ~/personal/whiteboard
	FilePath     string // full path to .jsonl
	Title        string
	StartedAt    time.Time
	ModifiedAt   time.Time
	GitBranch    string
	Version      string
	HasSubagents bool
}

// claudeProjectsDir returns the path to ~/.claude/projects/
func claudeProjectsDir() string {
	home, _ := os.UserHomeDir()
	return filepath.Join(home, ".claude", "projects")
}

// ListLocalSessions scans all Claude project directories for sessions.
func ListLocalSessions() ([]LocalSession, error) {
	projectsDir := claudeProjectsDir()
	projectDirs, err := os.ReadDir(projectsDir)
	if err != nil {
		return nil, fmt.Errorf("read claude projects dir: %w", err)
	}

	var sessions []LocalSession
	for _, pd := range projectDirs {
		if !pd.IsDir() {
			continue
		}
		dirPath := filepath.Join(projectsDir, pd.Name())
		dirSessions, err := listSessionsInProject(pd.Name(), dirPath)
		if err != nil {
			continue // skip broken project dirs
		}
		sessions = append(sessions, dirSessions...)
	}

	// Sort by modified time, newest first
	sort.Slice(sessions, func(i, j int) bool {
		return sessions[i].ModifiedAt.After(sessions[j].ModifiedAt)
	})

	return sessions, nil
}

// ListSessionsForCWD lists sessions for the current working directory.
func ListSessionsForCWD(cwd string) ([]LocalSession, error) {
	projectDir := cwdToProjectDir(cwd)
	dirPath := filepath.Join(claudeProjectsDir(), projectDir)

	if _, err := os.Stat(dirPath); os.IsNotExist(err) {
		return nil, fmt.Errorf("no Claude sessions found for %s", cwd)
	}

	sessions, err := listSessionsInProject(projectDir, dirPath)
	if err != nil {
		return nil, err
	}

	sort.Slice(sessions, func(i, j int) bool {
		return sessions[i].ModifiedAt.After(sessions[j].ModifiedAt)
	})

	return sessions, nil
}

// ResolveSession finds a session by ID (prefix match) or returns the most recent for cwd.
func ResolveSession(sessionID string) (*LocalSession, error) {
	if sessionID != "" {
		return findSessionByID(sessionID)
	}

	// Default: most recent session for current directory
	cwd, err := os.Getwd()
	if err != nil {
		return nil, fmt.Errorf("get working directory: %w", err)
	}

	sessions, err := ListSessionsForCWD(cwd)
	if err != nil {
		return nil, err
	}

	if len(sessions) == 0 {
		return nil, fmt.Errorf("no Claude sessions found in current directory")
	}

	return &sessions[0], nil
}

func findSessionByID(id string) (*LocalSession, error) {
	all, err := ListLocalSessions()
	if err != nil {
		return nil, err
	}

	var matches []LocalSession
	for _, s := range all {
		if strings.HasPrefix(s.ID, id) {
			matches = append(matches, s)
		}
	}

	switch len(matches) {
	case 0:
		return nil, fmt.Errorf("no session found matching %q", id)
	case 1:
		return &matches[0], nil
	default:
		return nil, fmt.Errorf("ambiguous session ID %q — matches %d sessions", id, len(matches))
	}
}

func listSessionsInProject(projectDirName, dirPath string) ([]LocalSession, error) {
	entries, err := os.ReadDir(dirPath)
	if err != nil {
		return nil, err
	}

	projectPath := projectDirToPath(projectDirName)

	var sessions []LocalSession
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".jsonl") {
			continue
		}

		filePath := filepath.Join(dirPath, e.Name())
		sessionID := strings.TrimSuffix(e.Name(), ".jsonl")

		info, err := e.Info()
		if err != nil {
			continue
		}

		// Check for subagents
		subagentDir := filepath.Join(dirPath, sessionID, "subagents")
		hasSubagents := false
		if _, err := os.Stat(subagentDir); err == nil {
			hasSubagents = true
		}

		// Read metadata from first few lines
		meta := readSessionMeta(filePath)

		sessions = append(sessions, LocalSession{
			ID:           sessionID,
			ShortID:      shortID(sessionID),
			ProjectDir:   projectDirName,
			ProjectPath:  projectPath,
			FilePath:     filePath,
			Title:        meta.title,
			StartedAt:    meta.startedAt,
			ModifiedAt:   info.ModTime(),
			GitBranch:    meta.gitBranch,
			Version:      meta.version,
			HasSubagents: hasSubagents,
		})
	}

	return sessions, nil
}

type sessionMeta struct {
	title     string
	startedAt time.Time
	gitBranch string
	version   string
}

func readSessionMeta(path string) sessionMeta {
	var meta sessionMeta

	f, err := os.Open(path)
	if err != nil {
		return meta
	}
	defer f.Close()

	// Read up to 20 lines to find metadata
	buf := make([]byte, 0, 4096)
	tmp := make([]byte, 4096)
	n, _ := f.Read(tmp)
	buf = append(buf, tmp[:n]...)

	lines := strings.Split(string(buf), "\n")
	for _, line := range lines {
		if line == "" {
			continue
		}
		var entry RawEntry
		if err := json.Unmarshal([]byte(line), &entry); err != nil {
			continue
		}

		// Get session metadata from first entry with these fields
		if meta.version == "" && entry.Version != "" {
			meta.version = entry.Version
			meta.gitBranch = entry.GitBranch
			if t, err := time.Parse(time.RFC3339Nano, entry.Timestamp); err == nil {
				meta.startedAt = t
			}
		}

		// Get title from first real user message
		if meta.title == "" && entry.Type == "user" && entry.Message != nil {
			msg, err := ParseMessage(entry.Message)
			if err != nil {
				continue
			}
			if IsStringContent(msg.Content) {
				text, err := ParseStringContent(msg.Content)
				if err != nil {
					continue
				}
				meta.title = truncateTitle(text)
				break
			}
		}
	}

	return meta
}

func truncateTitle(s string) string {
	// Take first line only
	if idx := strings.IndexByte(s, '\n'); idx >= 0 {
		s = s[:idx]
	}
	if len(s) > 80 {
		s = s[:80] + "..."
	}
	return s
}

// cwdToProjectDir converts a cwd path to Claude's project dir name.
// /Users/ashmaster/personal/whiteboard → -Users-ashmaster-personal-whiteboard
func cwdToProjectDir(cwd string) string {
	return "-" + strings.ReplaceAll(strings.TrimPrefix(cwd, "/"), "/", "-")
}

// projectDirToPath converts Claude's project dir name back to a readable path.
// -Users-ashmaster-personal-whiteboard → ~/personal/whiteboard
func projectDirToPath(dir string) string {
	// Strip leading -
	path := strings.TrimPrefix(dir, "-")
	// Replace - with /
	path = "/" + strings.ReplaceAll(path, "-", "/")
	return SanitizeString(path)
}

func shortID(id string) string {
	if len(id) > 8 {
		return id[:8]
	}
	return id
}
