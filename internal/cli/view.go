package cli

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"strings"

	"github.com/ashmaster/promptrail/internal/api"
	"github.com/ashmaster/promptrail/internal/auth"
	"github.com/ashmaster/promptrail/internal/parser"
	"github.com/charmbracelet/bubbles/viewport"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
)

func View(sessionID string, expandAgents, showThinking, raw bool, backendURL string) error {
	var processed *parser.ProcessedSession

	// username/session-id → remote view
	if parts := strings.SplitN(sessionID, "/", 2); len(parts) == 2 {
		p, err := fetchRemoteSession(backendURL, parts[0], parts[1])
		if err != nil {
			return err
		}
		processed = p
	} else {
		session, err := parser.ResolveSession(sessionID)
		if err != nil {
			return err
		}
		p, err := parser.ProcessSession(session.FilePath)
		if err != nil {
			return fmt.Errorf("process session: %w", err)
		}
		processed = p
	}

	if raw {
		enc := json.NewEncoder(os.Stdout)
		enc.SetIndent("", "  ")
		return enc.Encode(processed)
	}

	username := getUsername()

	m := newViewModel(processed, expandAgents, showThinking, username)
	p := tea.NewProgram(m, tea.WithAltScreen(), tea.WithMouseCellMotion())
	_, err := p.Run()
	return err
}

func fetchRemoteSession(backendURL, username, sessionID string) (*parser.ProcessedSession, error) {
	token := ""
	if creds, err := auth.LoadCredentials(); err == nil {
		token = creds.Token
	}

	client := api.NewClient(backendURL, token)

	resp, err := client.GetSessionByUser(username, sessionID)
	if err != nil {
		return nil, fmt.Errorf("get session: %w", err)
	}

	if resp.BlobURL == "" {
		return nil, fmt.Errorf("no blob available for this session")
	}

	data, err := client.FetchBlob(resp.BlobURL)
	if err != nil {
		return nil, fmt.Errorf("fetch blob: %w", err)
	}

	var processed parser.ProcessedSession
	if err := json.Unmarshal(data, &processed); err != nil {
		return nil, fmt.Errorf("parse session: %w", err)
	}

	return &processed, nil
}

// --- view model ---

var (
	viewTitleStyle = lipgloss.NewStyle().
			Bold(true).
			Foreground(lipgloss.Color("#FAFAFA")).
			Background(lipgloss.Color("#7D56F4")).
			Padding(0, 1)

	viewInfoStyle = lipgloss.NewStyle().
			Foreground(lipgloss.Color("241"))

	viewHelpStyle = lipgloss.NewStyle().
			Foreground(lipgloss.Color("241"))

	viewUserStyle = lipgloss.NewStyle().
			Bold(true).
			Foreground(lipgloss.Color("#FAFAFA")).
			Background(lipgloss.Color("#3B82F6")).
			Padding(0, 1)

	viewAssistantStyle = lipgloss.NewStyle().
				Bold(true).
				Foreground(lipgloss.Color("#FAFAFA")).
				Background(lipgloss.Color("#22C55E")).
				Padding(0, 1)

	viewTimestampStyle = lipgloss.NewStyle().
				Foreground(lipgloss.Color("241"))

	viewSeparatorStyle = lipgloss.NewStyle().
				Foreground(lipgloss.Color("238"))

	viewToolHeaderStyle = lipgloss.NewStyle().
				Foreground(lipgloss.Color("241"))

	viewToolBorderStyle = lipgloss.NewStyle().
				Foreground(lipgloss.Color("238"))

	viewToolInputStyle = lipgloss.NewStyle().
				Foreground(lipgloss.Color("36")) // cyan

	viewToolResultStyle = lipgloss.NewStyle().
				Foreground(lipgloss.Color("241"))

	viewAgentStyle = lipgloss.NewStyle().
			Bold(true).
			Foreground(lipgloss.Color("214")) // yellow/orange
)

type viewModel struct {
	viewport     viewport.Model
	content      string
	session      *parser.ProcessedSession
	username     string
	ready        bool
	width        int
	height       int
}

func newViewModel(session *parser.ProcessedSession, expandAgents, showThinking bool, username string) viewModel {
	return viewModel{
		session:  session,
		username: username,
	}
}

func (m viewModel) Init() tea.Cmd {
	return nil
}

func (m viewModel) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.width = msg.Width
		m.height = msg.Height

		headerHeight := 5 // header box
		footerHeight := 1 // help line
		verticalMargin := headerHeight + footerHeight

		if !m.ready {
			m.viewport = viewport.New(msg.Width, msg.Height-verticalMargin)
			m.viewport.YPosition = headerHeight
			m.content = renderContent(m.session, m.username, msg.Width)
			m.viewport.SetContent(m.content)
			m.ready = true
		} else {
			m.viewport.Width = msg.Width
			m.viewport.Height = msg.Height - verticalMargin
			m.content = renderContent(m.session, m.username, msg.Width)
			m.viewport.SetContent(m.content)
		}

	case tea.KeyMsg:
		switch msg.String() {
		case "q", "esc", "ctrl+c":
			return m, tea.Quit
		}
	}

	var cmd tea.Cmd
	m.viewport, cmd = m.viewport.Update(msg)
	return m, cmd
}

func (m viewModel) View() string {
	if !m.ready {
		return "Loading..."
	}

	header := renderViewHeader(m.session, m.width)
	footer := renderViewFooter(m.viewport, m.width)

	return header + "\n" + m.viewport.View() + "\n" + footer
}

func renderViewHeader(session *parser.ProcessedSession, w int) string {
	project := session.Session.CWD
	branch := session.Session.GitBranch
	date := session.Session.StartedAt
	if len(date) > 10 {
		date = date[:10]
	}

	info := date + "  " + fmt.Sprintf("%d messages", session.Session.MessageCount) + "  Claude " + session.Session.Version
	title := project
	if branch != "" && branch != "HEAD" {
		title += " @ " + branch
	}

	boxStyle := lipgloss.NewStyle().
		Border(lipgloss.RoundedBorder()).
		BorderForeground(lipgloss.Color("62")).
		Padding(0, 1).
		Width(w - 4)

	content := lipgloss.NewStyle().Bold(true).Render(title) + "\n" +
		viewInfoStyle.Render(info)

	return boxStyle.Render(content)
}

func renderViewFooter(vp viewport.Model, w int) string {
	pct := fmt.Sprintf(" %3.f%% ", vp.ScrollPercent()*100)
	help := " ↑↓/scroll navigate · q quit "
	gap := strings.Repeat(" ", max(0, w-lipgloss.Width(help)-lipgloss.Width(pct)))
	return viewHelpStyle.Render(help + gap + pct)
}

// --- content rendering ---

func renderContent(session *parser.ProcessedSession, username string, w int) string {
	var buf bytes.Buffer
	r := &viewRenderer{w: w, buf: &buf}

	for i, msg := range session.Messages {
		if msg.Type == "user" && i > 0 {
			r.separator()
		}
		r.message(msg, username, 0)
	}

	// Footer
	r.buf.WriteString("\n")
	r.buf.WriteString(viewSeparatorStyle.Render(strings.Repeat("─", w-2)) + "\n")
	r.buf.WriteString(viewInfoStyle.Render(fmt.Sprintf("End of session %s", session.Session.ID[:8])) + "\n")

	return buf.String()
}

type viewRenderer struct {
	w   int
	buf *bytes.Buffer
}

func (r *viewRenderer) separator() {
	r.buf.WriteString("\n")
	r.buf.WriteString(viewSeparatorStyle.Render(strings.Repeat("━", r.w-2)) + "\n")
	r.buf.WriteString("\n")
}

func (r *viewRenderer) message(msg parser.Message, username string, depth int) {
	indent := strings.Repeat("  ", depth)
	contentW := r.w - (depth * 2) - 2
	if contentW < 20 {
		contentW = 20
	}

	switch msg.Type {
	case "user":
		ts := fmtTime(msg.Timestamp)
		r.buf.WriteString(indent + viewUserStyle.Render(username) + " " + viewTimestampStyle.Render(ts) + "\n")
		r.buf.WriteString("\n")
		for _, block := range msg.Blocks {
			if block.Type == "text" {
				r.wrappedText(indent, block.Text, contentW)
			}
		}
		r.buf.WriteString("\n")

	case "assistant":
		ts := fmtTime(msg.Timestamp)
		r.buf.WriteString(indent + viewAssistantStyle.Render("Claude") + " " + viewTimestampStyle.Render(ts) + "\n")
		r.buf.WriteString("\n")
		for _, block := range msg.Blocks {
			r.block(block, username, depth)
		}
		r.buf.WriteString("\n")
	}
}

func (r *viewRenderer) block(block parser.Block, username string, depth int) {
	indent := strings.Repeat("  ", depth)
	contentW := r.w - (depth * 2) - 2
	if contentW < 20 {
		contentW = 20
	}

	switch block.Type {
	case "thinking":
		// skip by default
	case "text":
		r.wrappedText(indent, block.Text, contentW)
	case "tool_call":
		r.toolCall(block, username, depth)
	}
}

func (r *viewRenderer) toolCall(block parser.Block, username string, depth int) {
	indent := strings.Repeat("  ", depth) + "  "
	boxW := r.w - (depth * 2) - 6
	if boxW < 20 {
		boxW = 20
	}

	inputSummary := summarizeInput(block.Tool, block.Input)

	// Agent — expanded with subagent
	if block.Tool == "Agent" && block.Subagent != nil {
		desc := inputSummary
		if len(desc) > boxW-14 {
			desc = desc[:boxW-14] + "..."
		}
		r.buf.WriteString("\n")
		header := "─ Agent: " + desc + " "
		headerPad := max(0, boxW-lipgloss.Width(header))
		r.buf.WriteString(indent + viewAgentStyle.Render("┌"+header+strings.Repeat("─", headerPad)+"┐") + "\n")

		for _, subMsg := range block.Subagent.Messages {
			r.message(subMsg, username, depth+2)
		}

		if block.Result != nil && block.Result.Output != "" {
			resultLine := truncate(block.Result.Output, boxW-10)
			r.buf.WriteString(indent + viewAgentStyle.Render("│") + " " + viewToolResultStyle.Render("Result: "+resultLine) + "\n")
		}
		r.buf.WriteString(indent + viewAgentStyle.Render("└"+strings.Repeat("─", boxW)+"┘") + "\n")
		r.buf.WriteString("\n")
		return
	}

	// Regular tool call
	toolLabel := toolIcon(block.Tool) + " " + block.Tool
	topPad := max(0, boxW-lipgloss.Width(toolLabel)-3)
	r.buf.WriteString(indent + viewToolBorderStyle.Render("┌ "+toolLabel+" "+strings.Repeat("─", topPad)) + "\n")

	if inputSummary != "" {
		display := inputSummary
		if block.Tool == "Bash" {
			display = "$ " + display
		}
		for _, line := range wrapLines(display, boxW-4) {
			r.buf.WriteString(indent + viewToolBorderStyle.Render("│") + " " + viewToolInputStyle.Render(line) + "\n")
		}
	}

	if block.Result != nil && block.Result.Output != "" {
		r.buf.WriteString(indent + viewToolBorderStyle.Render("├"+strings.Repeat("─", boxW)) + "\n")
		lines := strings.Split(block.Result.Output, "\n")

		maxLines := 8
		if len(block.Result.Output) < 500 {
			maxLines = len(lines)
		}

		shown := 0
		for _, line := range lines {
			if shown >= maxLines {
				remaining := len(lines) - shown
				r.buf.WriteString(indent + viewToolBorderStyle.Render("│") + " " + viewToolResultStyle.Render(fmt.Sprintf("... %d more lines", remaining)) + "\n")
				break
			}
			if len(line) > boxW-2 {
				line = line[:boxW-5] + "..."
			}
			r.buf.WriteString(indent + viewToolBorderStyle.Render("│") + " " + viewToolResultStyle.Render(line) + "\n")
			shown++
		}

		if block.Result.Truncated {
			r.buf.WriteString(indent + viewToolBorderStyle.Render("│") + " " + viewToolResultStyle.Render(fmt.Sprintf("⚠ truncated (original: %s)", formatBytes(block.Result.OriginalBytes))) + "\n")
		}
	}

	r.buf.WriteString(indent + viewToolBorderStyle.Render("└"+strings.Repeat("─", boxW)) + "\n")
}

func (r *viewRenderer) wrappedText(indent, text string, maxW int) {
	for _, line := range strings.Split(text, "\n") {
		if lipgloss.Width(line) <= maxW {
			r.buf.WriteString(indent + line + "\n")
			continue
		}
		for _, wl := range wrapLines(line, maxW) {
			r.buf.WriteString(indent + wl + "\n")
		}
	}
}

// --- helpers ---

func getUsername() string {
	if username, ok := auth.IsLoggedIn(); ok && username != "" {
		return username
	}
	if u := os.Getenv("USER"); u != "" {
		return u
	}
	return "You"
}

func wrapLines(s string, maxW int) []string {
	if len(s) <= maxW {
		return []string{s}
	}
	var lines []string
	for len(s) > maxW {
		breakAt := maxW
		for i := maxW; i > maxW/2; i-- {
			if i < len(s) && s[i] == ' ' {
				breakAt = i
				break
			}
		}
		lines = append(lines, s[:breakAt])
		s = s[breakAt:]
		if len(s) > 0 && s[0] == ' ' {
			s = s[1:]
		}
	}
	if len(s) > 0 {
		lines = append(lines, s)
	}
	return lines
}

func summarizeInput(tool string, input any) string {
	m, ok := input.(map[string]any)
	if !ok {
		return ""
	}
	switch tool {
	case "Bash":
		if cmd, ok := m["command"].(string); ok {
			return truncate(cmd, 120)
		}
	case "Read":
		if fp, ok := m["file_path"].(string); ok {
			return fp
		}
	case "Write":
		if fp, ok := m["file_path"].(string); ok {
			return fp
		}
	case "Edit":
		if fp, ok := m["file_path"].(string); ok {
			return fp
		}
	case "Grep":
		if pat, ok := m["pattern"].(string); ok {
			path, _ := m["path"].(string)
			if path != "" {
				return fmt.Sprintf("/%s/ in %s", pat, path)
			}
			return fmt.Sprintf("/%s/", pat)
		}
	case "Glob":
		if pat, ok := m["pattern"].(string); ok {
			return pat
		}
	case "Agent":
		if desc, ok := m["description"].(string); ok {
			return desc
		}
	case "Skill":
		if skill, ok := m["skill"].(string); ok {
			return "/" + skill
		}
	case "TaskCreate":
		if subj, ok := m["subject"].(string); ok {
			return subj
		}
	case "TaskUpdate":
		if id, ok := m["taskId"].(string); ok {
			if status, ok := m["status"].(string); ok {
				return fmt.Sprintf("#%s → %s", id, status)
			}
			return "#" + id
		}
	}
	return ""
}

func toolIcon(tool string) string {
	switch tool {
	case "Bash":
		return "$"
	case "Read":
		return ">"
	case "Write":
		return "<"
	case "Edit":
		return "~"
	case "Grep":
		return "?"
	case "Glob":
		return "*"
	case "Agent":
		return "@"
	default:
		return "#"
	}
}

func truncate(s string, maxLen int) string {
	if idx := strings.IndexByte(s, '\n'); idx >= 0 {
		s = s[:idx]
	}
	if len(s) > maxLen {
		return s[:maxLen] + "..."
	}
	return s
}

func fmtTime(ts string) string {
	if len(ts) >= 16 {
		return ts[11:16]
	}
	return ts
}

func formatBytes(b int) string {
	if b < 1024 {
		return fmt.Sprintf("%d B", b)
	}
	if b < 1024*1024 {
		return fmt.Sprintf("%.1f KB", float64(b)/1024)
	}
	return fmt.Sprintf("%.1f MB", float64(b)/(1024*1024))
}
