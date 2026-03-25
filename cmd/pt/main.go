package main

import (
	"fmt"
	"os"

	"github.com/ashmaster/promptrail/internal/cli"
	"github.com/spf13/cobra"
)

var (
	version = "dev"
	commit  = "none"
)

const defaultBackendURL = "https://promptrail.fly.dev"

func main() {
	backendURL := os.Getenv("PT_BACKEND_URL")
	if backendURL == "" {
		backendURL = defaultBackendURL
	}

	rootCmd := &cobra.Command{
		Use:     "pt",
		Short:   "PromptRail — upload, share, and view Claude Code sessions",
		Version: version + " (" + commit + ")",
	}

	// login
	var forceLogin bool
	loginCmd := &cobra.Command{
		Use:   "login",
		Short: "Authenticate with GitHub",
		RunE: func(cmd *cobra.Command, args []string) error {
			return cli.Login(backendURL, forceLogin)
		},
	}
	loginCmd.Flags().BoolVar(&forceLogin, "force", false, "Re-login even if already authenticated")

	// logout
	logoutCmd := &cobra.Command{
		Use:   "logout",
		Short: "Remove stored credentials",
		RunE: func(cmd *cobra.Command, args []string) error {
			return cli.Logout()
		},
	}

	// list
	var listRemote bool
	listCmd := &cobra.Command{
		Use:   "list",
		Short: "Browse Claude sessions",
		RunE: func(cmd *cobra.Command, args []string) error {
			if listRemote {
				return cli.ListRemote(backendURL)
			}
			return cli.ListLocal(backendURL)
		},
	}
	listCmd.Flags().BoolVar(&listRemote, "remote", false, "Browse uploaded sessions")

	// upload
	var uploadTitle, uploadAccess string
	uploadCmd := &cobra.Command{
		Use:   "upload [session-id]",
		Short: "Upload a Claude session",
		Long:  "Upload a session to the server. If no session ID is given, opens a picker.",
		Args:  cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			sessionID := ""
			if len(args) > 0 {
				sessionID = args[0]
			}
			return cli.Upload(backendURL, sessionID, uploadTitle, uploadAccess)
		},
	}
	uploadCmd.Flags().StringVar(&uploadTitle, "title", "", "Override session title")
	uploadCmd.Flags().StringVar(&uploadAccess, "access", "", "Set access level (private|public)")

	// view
	var viewExpandAgents, viewShowThinking, viewRaw bool
	viewCmd := &cobra.Command{
		Use:   "view [session-id | username/session-id]",
		Short: "View a Claude session",
		Long:  "View a local session by ID, or a remote session with username/session-id (e.g., ashmaster/df0712b1)",
		Args:  cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			sessionID := ""
			if len(args) > 0 {
				sessionID = args[0]
			}
			return cli.View(sessionID, viewExpandAgents, viewShowThinking, viewRaw, backendURL)
		},
	}
	viewCmd.Flags().BoolVar(&viewExpandAgents, "expand-agents", false, "Show full subagent conversations inline")
	viewCmd.Flags().BoolVar(&viewShowThinking, "show-thinking", false, "Show Claude's thinking blocks")
	viewCmd.Flags().BoolVar(&viewRaw, "raw", false, "Output raw processed JSON")

	rootCmd.AddCommand(loginCmd, logoutCmd, listCmd, uploadCmd, viewCmd)

	if err := rootCmd.Execute(); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}
