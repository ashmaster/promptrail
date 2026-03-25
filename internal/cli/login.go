package cli

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"net"
	"net/http"
	"os"
	"time"

	"github.com/ashmaster/promptrail/internal/auth"
	"github.com/charmbracelet/bubbles/spinner"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/golang-jwt/jwt/v5"
	"github.com/pkg/browser"
)

const (
	loginTimeout  = 5 * time.Minute
	preferredPort = 9876
)

type loginResult struct {
	Token    string
	Nonce    string
	Username string
	Error    string
}

func Login(backendURL string, force bool) error {
	if !force {
		if username, ok := auth.IsLoggedIn(); ok {
			fmt.Fprintf(os.Stderr, "Already logged in as %s. Use --force to re-login.\n", username)
			return nil
		}
	}

	m := newLoginModel(backendURL)
	p := tea.NewProgram(m, tea.WithAltScreen())
	_, err := p.Run()
	return err
}

// Logout removes stored credentials.
func Logout() error {
	if _, ok := auth.IsLoggedIn(); !ok {
		fmt.Fprintln(os.Stderr, "Not logged in")
		return nil
	}
	if err := auth.DeleteCredentials(); err != nil {
		return err
	}
	fmt.Fprintln(os.Stderr, "Logged out")
	return nil
}

// --- login model ---

type loginState int

const (
	loginStateWaiting loginState = iota
	loginStateSuccess
	loginStateError
)

type loginModel struct {
	backendURL string
	spinner    spinner.Model
	state      loginState
	width      int
	height     int
	username   string
	errMsg     string
	loginURL   string
}

type loginSuccessMsg struct {
	username string
}

type loginErrorMsg struct {
	err string
}

func newLoginModel(backendURL string) loginModel {
	s := spinner.New()
	s.Spinner = spinner.Dot
	s.Style = lipgloss.NewStyle().Foreground(lipgloss.Color("205"))

	return loginModel{
		backendURL: backendURL,
		spinner:    s,
	}
}

func (m loginModel) Init() tea.Cmd {
	return tea.Batch(m.spinner.Tick, m.startOAuth())
}

func (m loginModel) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.width = msg.Width
		m.height = msg.Height

	case tea.KeyMsg:
		switch msg.String() {
		case "q", "esc", "ctrl+c":
			return m, tea.Quit
		default:
			if m.state == loginStateSuccess || m.state == loginStateError {
				return m, tea.Quit
			}
		}

	case spinner.TickMsg:
		var cmd tea.Cmd
		m.spinner, cmd = m.spinner.Update(msg)
		return m, cmd

	case loginSuccessMsg:
		m.state = loginStateSuccess
		m.username = msg.username
		return m, nil

	case loginErrorMsg:
		m.state = loginStateError
		m.errMsg = msg.err
		return m, nil
	}

	return m, nil
}

func (m loginModel) View() string {
	var s string

	s += "\n"

	switch m.state {
	case loginStateWaiting:
		s += "  " + titleStyle.Render("Login") + "\n\n"
		s += "  " + m.spinner.View() + " Waiting for GitHub authorization...\n\n"
		if m.loginURL != "" {
			s += "  " + helpStyle.Render("Open this URL if the browser didn't open:") + "\n"
			urlStyle := lipgloss.NewStyle().Foreground(lipgloss.Color("36"))
			s += "  " + urlStyle.Render(m.loginURL) + "\n\n"
		}
		s += "  " + helpStyle.Render("press q to cancel")

	case loginStateSuccess:
		s += "  " + titleStyle.Render("Login") + "\n\n"
		check := lipgloss.NewStyle().Foreground(lipgloss.Color("42")).Bold(true).Render("✓")
		s += "  " + check + " Logged in as " + lipgloss.NewStyle().Bold(true).Render(m.username) + "\n\n"
		s += "  " + helpStyle.Render("press any key to exit")

	case loginStateError:
		s += "  " + lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("196")).Background(lipgloss.Color("#FAFAFA")).Padding(0, 1).Render("Login Failed") + "\n\n"
		s += "  " + lipgloss.NewStyle().Foreground(lipgloss.Color("196")).Render(m.errMsg) + "\n\n"
		s += "  " + helpStyle.Render("press any key to exit")
	}

	return s
}

// --- OAuth flow as tea.Cmd ---

func (m *loginModel) startOAuth() tea.Cmd {
	return func() tea.Msg {
		// Generate nonce
		nonceBytes := make([]byte, 32)
		if _, err := rand.Read(nonceBytes); err != nil {
			return loginErrorMsg{err: fmt.Sprintf("generate nonce: %v", err)}
		}
		nonce := hex.EncodeToString(nonceBytes)

		// Find port
		port, listener, err := findPort()
		if err != nil {
			return loginErrorMsg{err: fmt.Sprintf("find free port: %v", err)}
		}

		resultCh := make(chan loginResult, 1)

		mux := http.NewServeMux()
		mux.HandleFunc("/callback", func(w http.ResponseWriter, r *http.Request) {
			result := loginResult{
				Token:    r.URL.Query().Get("token"),
				Nonce:    r.URL.Query().Get("nonce"),
				Username: r.URL.Query().Get("username"),
				Error:    r.URL.Query().Get("error"),
			}
			if result.Error != "" {
				w.Header().Set("Content-Type", "text/html; charset=utf-8")
				fmt.Fprint(w, `<!DOCTYPE html><html><body><h2>Login Cancelled</h2><p>You can close this tab.</p></body></html>`)
			} else {
				w.Header().Set("Content-Type", "text/html; charset=utf-8")
				fmt.Fprint(w, `<!DOCTYPE html><html><body><h2>Login Successful</h2><p>You can close this tab.</p></body></html>`)
			}
			resultCh <- result
		})

		srv := &http.Server{Handler: mux}
		go srv.Serve(listener)
		defer func() {
			ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
			defer cancel()
			srv.Shutdown(ctx)
		}()

		loginURL := fmt.Sprintf(
			"%s/auth/github?nonce=%s&callback_port=%d",
			m.backendURL, nonce, port,
		)

		m.loginURL = loginURL
		browser.OpenURL(loginURL)

		select {
		case result := <-resultCh:
			if result.Error != "" {
				return loginErrorMsg{err: fmt.Sprintf("login cancelled: %s", result.Error)}
			}
			if result.Nonce != nonce {
				return loginErrorMsg{err: "nonce mismatch (possible CSRF attack)"}
			}

			parser := jwt.NewParser(jwt.WithoutClaimsValidation())
			token, _, err := parser.ParseUnverified(result.Token, &jwt.RegisteredClaims{})
			if err != nil {
				return loginErrorMsg{err: fmt.Sprintf("parse token: %v", err)}
			}
			claims, ok := token.Claims.(*jwt.RegisteredClaims)
			if !ok || claims.ExpiresAt == nil {
				return loginErrorMsg{err: "invalid token claims"}
			}

			creds := &auth.Credentials{
				Token:     result.Token,
				Username:  result.Username,
				ExpiresAt: claims.ExpiresAt.Time,
			}
			if err := auth.SaveCredentials(creds); err != nil {
				return loginErrorMsg{err: fmt.Sprintf("save credentials: %v", err)}
			}

			return loginSuccessMsg{username: result.Username}

		case <-time.After(loginTimeout):
			return loginErrorMsg{err: "login timed out"}
		}
	}
}

func findPort() (int, net.Listener, error) {
	if l, err := net.Listen("tcp", fmt.Sprintf("127.0.0.1:%d", preferredPort)); err == nil {
		return preferredPort, l, nil
	}
	for i := 0; i < 3; i++ {
		l, err := net.Listen("tcp", "127.0.0.1:0")
		if err == nil {
			port := l.Addr().(*net.TCPAddr).Port
			return port, l, nil
		}
	}
	return 0, nil, fmt.Errorf("could not find a free port")
}
