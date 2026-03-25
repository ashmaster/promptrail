package cli

import (
	"fmt"
	"os"
	"strings"

	"github.com/ashmaster/promptrail/internal/api"
	"github.com/ashmaster/promptrail/internal/auth"
	"github.com/ashmaster/promptrail/internal/parser"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
)

// --- local list ---

func ListLocal(backendURL string) error {
	sessions, err := parser.ListLocalSessions()
	if err != nil {
		return err
	}

	if len(sessions) == 0 {
		fmt.Fprintln(os.Stderr, "No local Claude sessions found.")
		return nil
	}

	m := newLocalListModel(sessions, backendURL)
	p := tea.NewProgram(m, tea.WithAltScreen())
	final, err := p.Run()
	if err != nil {
		return err
	}

	if fm, ok := final.(localListModel); ok {
		switch fm.action {
		case "view":
			return View(fm.actionTarget, false, false, false, backendURL)
		case "upload":
			return Upload(backendURL, fm.actionTarget, "", "")
		}
	}
	return nil
}

type localListModel struct {
	sessions     []parser.LocalSession
	backendURL   string
	cursor       int
	width        int
	height       int
	action       string // "view" or "upload" — set on exit
	actionTarget string // session ID
}

func newLocalListModel(sessions []parser.LocalSession, backendURL string) localListModel {
	return localListModel{sessions: sessions, backendURL: backendURL}
}

func (m localListModel) Init() tea.Cmd { return nil }

func (m localListModel) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.width = msg.Width
		m.height = msg.Height

	case tea.KeyMsg:
		switch msg.String() {
		case "q", "esc", "ctrl+c":
			return m, tea.Quit
		case "up", "k":
			if m.cursor > 0 {
				m.cursor--
			}
		case "down", "j":
			if m.cursor < len(m.sessions)-1 {
				m.cursor++
			}
		case "enter":
			m.action = "view"
			m.actionTarget = m.sessions[m.cursor].ID
			return m, tea.Quit
		case "u":
			m.action = "upload"
			m.actionTarget = m.sessions[m.cursor].ID
			return m, tea.Quit
		}
	}
	return m, nil
}

func (m localListModel) View() string {
	if m.width == 0 {
		return "Loading..."
	}

	var b strings.Builder

	b.WriteString("\n")
	b.WriteString("  " + titleStyle.Render("Local Sessions") + "\n\n")

	// Header
	header := m.formatRow("ID", "PROJECT", "DATE", "TITLE")
	b.WriteString("  " + headerStyle.Render(header) + "\n")
	b.WriteString("  " + separatorStyle.Render(strings.Repeat("─", m.width-4)) + "\n")

	// Rows
	maxRows := m.height - 8
	if maxRows < 1 {
		maxRows = 1
	}

	scrollStart := 0
	if m.cursor >= maxRows {
		scrollStart = m.cursor - maxRows + 1
	}

	rowsDrawn := 0
	for i := scrollStart; i < len(m.sessions) && rowsDrawn < maxRows; i++ {
		s := m.sessions[i]
		title := s.Title
		if title == "" {
			title = "(no title)"
		}
		row := m.formatRow(s.ShortID, s.ProjectPath, s.ModifiedAt.Format("2006-01-02"), title)

		if i == m.cursor {
			padded := row + strings.Repeat(" ", max(0, m.width-4-lipgloss.Width(row)))
			b.WriteString("  " + selectedStyle.Render(padded) + "\n")
		} else {
			b.WriteString("  " + normalStyle.Render(row) + "\n")
		}
		rowsDrawn++
	}

	for rowsDrawn < maxRows {
		b.WriteString("\n")
		rowsDrawn++
	}

	b.WriteString("\n")
	help := "↑↓ navigate · enter view · u upload · q quit"
	count := fmt.Sprintf("%d/%d", m.cursor+1, len(m.sessions))
	gap := max(0, m.width-4-len(help)-len(count))
	b.WriteString("  " + helpStyle.Render(help+strings.Repeat(" ", gap)+count))

	return b.String()
}

func (m localListModel) formatRow(id, project, date, title string) string {
	titleW := m.width - 4 - 10 - 30 - 12 - 6
	if titleW < 10 {
		titleW = 10
	}
	if len(title) > titleW {
		title = title[:titleW-3] + "..."
	}
	if len(project) > 28 {
		project = project[:25] + "..."
	}
	return fmt.Sprintf("%-10s %-28s %-12s %-*s", id, project, date, titleW, title)
}

// --- remote list ---

func ListRemote(backendURL string) error {
	creds, err := auth.LoadCredentials()
	if err != nil {
		return err
	}

	client := api.NewClient(backendURL, creds.Token)
	resp, err := client.ListSessions()
	if err != nil {
		return fmt.Errorf("list remote sessions: %w", err)
	}

	if len(resp.Sessions) == 0 {
		fmt.Fprintln(os.Stderr, "No uploaded sessions.")
		return nil
	}

	m := newRemoteListModel(client, resp.Sessions, backendURL, creds.Username)
	p := tea.NewProgram(m, tea.WithAltScreen())
	final, err := p.Run()
	if err != nil {
		return err
	}

	if fm, ok := final.(remoteListModel); ok && fm.viewSession != "" {
		return View(fm.viewSession, false, false, false, backendURL)
	}
	return nil
}

// --- styles (shared) ---

var (
	titleStyle = lipgloss.NewStyle().
			Bold(true).
			Foreground(lipgloss.Color("#FAFAFA")).
			Background(lipgloss.Color("#7D56F4")).
			Padding(0, 1)

	headerStyle = lipgloss.NewStyle().
			Foreground(lipgloss.Color("241")).
			Bold(true)

	selectedStyle = lipgloss.NewStyle().
			Foreground(lipgloss.Color("#FAFAFA")).
			Background(lipgloss.Color("62")).
			Bold(true)

	normalStyle = lipgloss.NewStyle()

	accessPublicStyle = lipgloss.NewStyle().
				Foreground(lipgloss.Color("42")).
				Bold(true)

	accessPrivateStyle = lipgloss.NewStyle().
				Foreground(lipgloss.Color("241"))

	helpStyle = lipgloss.NewStyle().
			Foreground(lipgloss.Color("241"))

	statusMsgStyle = lipgloss.NewStyle().
			Foreground(lipgloss.Color("214")).
			Bold(true)

	separatorStyle = lipgloss.NewStyle().
			Foreground(lipgloss.Color("238"))
)

// --- remote list model ---

type remoteListModel struct {
	client      *api.Client
	sessions    []api.SessionInfo
	backendURL  string
	username    string
	cursor      int
	width       int
	height      int
	message     string
	viewSession string
}

func newRemoteListModel(client *api.Client, sessions []api.SessionInfo, backendURL, username string) remoteListModel {
	return remoteListModel{
		client:     client,
		sessions:   sessions,
		backendURL: backendURL,
		username:   username,
	}
}

func (m remoteListModel) Init() tea.Cmd { return nil }

func (m remoteListModel) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.width = msg.Width
		m.height = msg.Height

	case tea.KeyMsg:
		m.message = ""
		switch msg.String() {
		case "q", "esc", "ctrl+c":
			return m, tea.Quit
		case "up", "k":
			if m.cursor > 0 {
				m.cursor--
			}
		case "down", "j":
			if m.cursor < len(m.sessions)-1 {
				m.cursor++
			}
		case "enter":
			s := m.sessions[m.cursor]
			csid := s.ClaudeSessionID
			if csid == "" {
				csid = s.ID
			}
			if len(csid) > 8 {
				csid = csid[:8]
			}
			m.viewSession = fmt.Sprintf("%s/%s", m.username, csid)
			return m, tea.Quit
		case "p":
			s := &m.sessions[m.cursor]
			newAccess := "public"
			if s.Access == "public" {
				newAccess = "private"
			}
			if err := m.client.UpdateAccess(s.ID, newAccess); err != nil {
				m.message = fmt.Sprintf("Error: %v", err)
			} else {
				s.Access = newAccess
				m.message = fmt.Sprintf("Set to %s", newAccess)
			}
		}
	}
	return m, nil
}

func (m remoteListModel) View() string {
	if m.width == 0 {
		return "Loading..."
	}

	var b strings.Builder

	b.WriteString("\n")
	b.WriteString("  " + titleStyle.Render("Uploaded Sessions") + "\n\n")

	header := m.formatRow("ID", "TITLE", "ACCESS", "DATE")
	b.WriteString("  " + headerStyle.Render(header) + "\n")
	b.WriteString("  " + separatorStyle.Render(strings.Repeat("─", m.width-4)) + "\n")

	maxRows := m.height - 8
	if maxRows < 1 {
		maxRows = 1
	}

	scrollStart := 0
	if m.cursor >= maxRows {
		scrollStart = m.cursor - maxRows + 1
	}

	rowsDrawn := 0
	for i := scrollStart; i < len(m.sessions) && rowsDrawn < maxRows; i++ {
		s := m.sessions[i]
		date := ""
		if len(s.UploadedAt) >= 10 {
			date = s.UploadedAt[:10]
		}
		id := s.ClaudeSessionID
		if id == "" {
			id = s.ID
		}
		if len(id) > 8 {
			id = id[:8]
		}

		var accessStr string
		if s.Access == "public" {
			accessStr = accessPublicStyle.Render(fmt.Sprintf("%-10s", "public"))
		} else {
			accessStr = accessPrivateStyle.Render(fmt.Sprintf("%-10s", "private"))
		}

		titleW := m.width - 4 - 10 - 12 - 12 - 6
		if titleW < 10 {
			titleW = 10
		}
		title := s.Title
		if len(title) > titleW {
			title = title[:titleW-3] + "..."
		}

		row := fmt.Sprintf("%-10s %-*s %s %-12s", id, titleW, title, accessStr, date)

		if i == m.cursor {
			padded := row + strings.Repeat(" ", max(0, m.width-4-lipgloss.Width(row)))
			b.WriteString("  " + selectedStyle.Render(padded) + "\n")
		} else {
			b.WriteString("  " + normalStyle.Render(row) + "\n")
		}
		rowsDrawn++
	}

	for rowsDrawn < maxRows {
		b.WriteString("\n")
		rowsDrawn++
	}

	b.WriteString("\n")
	if m.message != "" {
		b.WriteString("  " + statusMsgStyle.Render(m.message) + "\n")
	} else {
		b.WriteString("\n")
	}

	help := "↑↓ navigate · enter view · p toggle public/private · q quit"
	count := fmt.Sprintf("%d/%d", m.cursor+1, len(m.sessions))
	gap := max(0, m.width-4-len(help)-len(count))
	b.WriteString("  " + helpStyle.Render(help+strings.Repeat(" ", gap)+count))

	return b.String()
}

func (m remoteListModel) formatRow(id, title, access, date string) string {
	titleW := m.width - 4 - 10 - 12 - 12 - 6
	if titleW < 10 {
		titleW = 10
	}
	return fmt.Sprintf("%-10s %-*s %-10s %-12s", id, titleW, title, access, date)
}

func max(a, b int) int {
	if a > b {
		return a
	}
	return b
}
