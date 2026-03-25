package cli

import (
	"bytes"
	"compress/gzip"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/ashmaster/promptrail/internal/api"
	"github.com/ashmaster/promptrail/internal/auth"
	"github.com/ashmaster/promptrail/internal/parser"
	"github.com/charmbracelet/bubbles/spinner"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
)

func Upload(backendURL, sessionID, title, access string) error {
	creds, err := auth.LoadCredentials()
	if err != nil {
		return err
	}

	// If no session ID, show picker
	if sessionID == "" {
		picked, err := pickSession(backendURL)
		if err != nil {
			return err
		}
		if picked == "" {
			return nil // cancelled
		}
		sessionID = picked
	}

	// Run upload with progress TUI
	m := newUploadModel(backendURL, creds, sessionID, title, access)
	p := tea.NewProgram(m, tea.WithAltScreen())
	_, err = p.Run()
	return err
}

func pickSession(backendURL string) (string, error) {
	sessions, err := parser.ListLocalSessions()
	if err != nil {
		return "", err
	}
	if len(sessions) == 0 {
		return "", fmt.Errorf("no local Claude sessions found")
	}

	m := newLocalListModel(sessions, backendURL)
	// Override: pressing enter returns session ID for upload
	p := tea.NewProgram(m, tea.WithAltScreen())
	final, err := p.Run()
	if err != nil {
		return "", err
	}

	if fm, ok := final.(localListModel); ok && fm.action == "upload" {
		return fm.actionTarget, nil
	}
	if fm, ok := final.(localListModel); ok && fm.action == "view" {
		// User pressed enter — in upload context, treat as select
		return fm.actionTarget, nil
	}
	return "", nil
}

// --- upload model ---

type uploadStep int

const (
	stepProcessing uploadStep = iota
	stepUploading
	stepConfirming
	stepDone
	stepError
)

type uploadModel struct {
	backendURL string
	creds      *auth.Credentials
	sessionID  string
	title      string
	access     string

	step         uploadStep
	spinner      spinner.Model
	width        int
	height       int
	sessionInfo  string // display info
	messageCount int
	blobSize     string
	shareURL     string
	username     string
	errMsg       string

	// Steps done
	steps []stepStatus
}

type stepStatus struct {
	name string
	done bool
}

// Messages
type processedMsg struct {
	processed *parser.ProcessedSession
	blob      []byte
	title     string
	err       error
}

type uploadedMsg struct {
	sessionID  string
	shareToken string
	err        error
}

type confirmedMsg struct {
	shareURL string
	err      error
}

func newUploadModel(backendURL string, creds *auth.Credentials, sessionID, title, access string) uploadModel {
	s := spinner.New()
	s.Spinner = spinner.Dot
	s.Style = lipgloss.NewStyle().Foreground(lipgloss.Color("205"))

	return uploadModel{
		backendURL: backendURL,
		creds:      creds,
		sessionID:  sessionID,
		title:      title,
		access:     access,
		spinner:    s,
		username:   creds.Username,
		steps: []stepStatus{
			{name: "Processing session"},
			{name: "Uploading blob"},
			{name: "Confirming upload"},
		},
	}
}

func (m uploadModel) Init() tea.Cmd {
	return tea.Batch(m.spinner.Tick, m.processSession())
}

func (m uploadModel) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.width = msg.Width
		m.height = msg.Height

	case tea.KeyMsg:
		switch msg.String() {
		case "q", "esc", "ctrl+c":
			return m, tea.Quit
		default:
			if m.step == stepDone || m.step == stepError {
				return m, tea.Quit
			}
		}

	case spinner.TickMsg:
		var cmd tea.Cmd
		m.spinner, cmd = m.spinner.Update(msg)
		return m, cmd

	case processedMsg:
		if msg.err != nil {
			m.step = stepError
			m.errMsg = msg.err.Error()
			return m, nil
		}
		m.steps[0].done = true
		m.messageCount = msg.processed.Session.MessageCount
		m.sessionInfo = msg.processed.Session.CWD
		m.blobSize = fmt.Sprintf("%.1f KB", float64(len(msg.blob))/1024)
		m.title = msg.title
		m.step = stepUploading
		return m, m.uploadBlob(msg.processed, msg.blob)

	case uploadedMsg:
		if msg.err != nil {
			m.step = stepError
			m.errMsg = msg.err.Error()
			return m, nil
		}
		m.steps[1].done = true
		m.step = stepConfirming
		return m, m.confirmUpload(msg.sessionID)

	case confirmedMsg:
		if msg.err != nil {
			m.step = stepError
			m.errMsg = msg.err.Error()
			return m, nil
		}
		m.steps[2].done = true
		m.shareURL = msg.shareURL
		m.step = stepDone
		return m, nil
	}

	return m, nil
}

func (m uploadModel) View() string {
	if m.width == 0 {
		return "Loading..."
	}

	var b strings.Builder

	b.WriteString("\n")

	if m.step == stepDone {
		b.WriteString("  " + titleStyle.Render("Upload Complete") + "\n\n")
	} else if m.step == stepError {
		b.WriteString("  " + lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("196")).Background(lipgloss.Color("#FAFAFA")).Padding(0, 1).Render("Upload Failed") + "\n\n")
	} else {
		b.WriteString("  " + titleStyle.Render("Uploading") + "\n\n")
	}

	// Session info
	if m.sessionInfo != "" {
		b.WriteString("  Session:  " + m.sessionID[:8] + "\n")
		b.WriteString("  Project:  " + m.sessionInfo + "\n")
		if m.title != "" {
			titleDisplay := m.title
			if len(titleDisplay) > m.width-14 {
				titleDisplay = titleDisplay[:m.width-17] + "..."
			}
			b.WriteString("  Title:    " + titleDisplay + "\n")
		}
		b.WriteString("\n")
	}

	// Steps
	checkDone := lipgloss.NewStyle().Foreground(lipgloss.Color("42")).Bold(true).Render("✓")
	checkFail := lipgloss.NewStyle().Foreground(lipgloss.Color("196")).Bold(true).Render("✗")

	for i, step := range m.steps {
		if step.done {
			detail := ""
			if i == 0 && m.messageCount > 0 {
				detail = fmt.Sprintf(" (%d messages)", m.messageCount)
			}
			if i == 1 && m.blobSize != "" {
				detail = fmt.Sprintf(" (%s)", m.blobSize)
			}
			b.WriteString("  " + checkDone + " " + step.name + detail + "\n")
		} else if m.step == stepError && int(m.step)-1 == i {
			b.WriteString("  " + checkFail + " " + step.name + "\n")
		} else if int(m.step) == i {
			b.WriteString("  " + m.spinner.View() + " " + step.name + "...\n")
		} else {
			b.WriteString("  " + lipgloss.NewStyle().Foreground(lipgloss.Color("241")).Render("○ "+step.name) + "\n")
		}
	}

	b.WriteString("\n")

	if m.step == stepDone {
		ref := fmt.Sprintf("%s/%s", m.username, m.sessionID[:8])
		cmdStyle := lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("36"))
		b.WriteString("  Share: " + cmdStyle.Render(ref) + "\n")
		b.WriteString("  View:  " + cmdStyle.Render("pt view "+ref) + "\n")
		b.WriteString("\n")
		b.WriteString("  " + helpStyle.Render("press any key to exit"))
	} else if m.step == stepError {
		b.WriteString("  " + lipgloss.NewStyle().Foreground(lipgloss.Color("196")).Render("Error: "+m.errMsg) + "\n")
		b.WriteString("\n")
		b.WriteString("  " + helpStyle.Render("press any key to exit"))
	} else {
		b.WriteString("  " + helpStyle.Render("press q to cancel"))
	}

	return b.String()
}

// --- async commands ---

func (m uploadModel) processSession() tea.Cmd {
	return func() tea.Msg {
		session, err := parser.ResolveSession(m.sessionID)
		if err != nil {
			return processedMsg{err: err}
		}

		processed, err := parser.ProcessSession(session.FilePath)
		if err != nil {
			return processedMsg{err: err}
		}

		title := m.title
		if title == "" {
			for _, msg := range processed.Messages {
				if msg.Type == "user" && len(msg.Blocks) > 0 {
					title = msg.Blocks[0].Text
					if len(title) > 80 {
						title = title[:80] + "..."
					}
					break
				}
			}
		}
		if title == "" {
			title = "Untitled session"
		}

		jsonData, err := json.Marshal(processed)
		if err != nil {
			return processedMsg{err: err}
		}

		var gzBuf bytes.Buffer
		gz := gzip.NewWriter(&gzBuf)
		gz.Write(jsonData)
		gz.Close()

		return processedMsg{
			processed: processed,
			blob:      gzBuf.Bytes(),
			title:     title,
		}
	}
}

func (m uploadModel) uploadBlob(processed *parser.ProcessedSession, blob []byte) tea.Cmd {
	return func() tea.Msg {
		client := api.NewClient(m.backendURL, m.creds.Token)

		createResp, err := client.CreateSession(&api.CreateSessionRequest{
			ClaudeSessionID: processed.Session.ID,
			Title:           m.title,
			ProjectPath:     processed.Session.CWD,
			BlobSizeBytes:   int64(len(blob)),
			MessageCount:    processed.Session.MessageCount,
			Metadata: map[string]string{
				"claude_version": processed.Session.Version,
				"git_branch":     processed.Session.GitBranch,
				"started_at":     processed.Session.StartedAt,
			},
		})
		if err != nil {
			return uploadedMsg{err: err}
		}

		if createResp.UploadURL != "" {
			if err := client.UploadBlob(createResp.UploadURL, blob); err != nil {
				return uploadedMsg{err: err}
			}
		}

		return uploadedMsg{sessionID: createResp.SessionID, shareToken: createResp.ShareToken}
	}
}

func (m uploadModel) confirmUpload(sessionID string) tea.Cmd {
	return func() tea.Msg {
		client := api.NewClient(m.backendURL, m.creds.Token)

		confirmResp, err := client.ConfirmUpload(sessionID, &api.ConfirmUploadRequest{
			BlobSizeBytes: 0, // already set during upload
			MessageCount:  m.messageCount,
		})
		if err != nil {
			return confirmedMsg{err: err}
		}

		if m.access != "" && m.access != "private" {
			client.UpdateAccess(sessionID, m.access)
		}

		return confirmedMsg{shareURL: confirmResp.ShareURL}
	}
}
