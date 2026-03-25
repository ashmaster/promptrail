package auth

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"time"
)

type Credentials struct {
	Token     string    `json:"token"`
	Username  string    `json:"username"`
	ExpiresAt time.Time `json:"expires_at"`
}

const configDir = ".config/promptrail"
const credentialsFile = "credentials.json"

func credentialsPath() string {
	home, _ := os.UserHomeDir()
	return filepath.Join(home, configDir, credentialsFile)
}

// LoadCredentials reads and validates the stored credentials.
func LoadCredentials() (*Credentials, error) {
	path := credentialsPath()
	data, err := os.ReadFile(path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil, fmt.Errorf("not logged in. Run: pt login")
		}
		return nil, fmt.Errorf("credentials corrupted. Run: pt login")
	}

	var creds Credentials
	if err := json.Unmarshal(data, &creds); err != nil {
		return nil, fmt.Errorf("credentials corrupted. Run: pt login")
	}

	if time.Now().After(creds.ExpiresAt) {
		return nil, fmt.Errorf("session expired. Run: pt login")
	}

	if time.Until(creds.ExpiresAt) < 24*time.Hour {
		fmt.Fprintf(os.Stderr, "Warning: session expires soon. Run: pt login to refresh\n")
	}

	return &creds, nil
}

// SaveCredentials writes credentials to disk with 0600 permissions.
func SaveCredentials(creds *Credentials) error {
	path := credentialsPath()

	// Ensure directory exists
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0700); err != nil {
		return fmt.Errorf("create config dir: %w", err)
	}

	data, err := json.MarshalIndent(creds, "", "  ")
	if err != nil {
		return fmt.Errorf("marshal credentials: %w", err)
	}

	if err := os.WriteFile(path, data, 0600); err != nil {
		return fmt.Errorf("write credentials: %w", err)
	}

	return nil
}

// DeleteCredentials removes the credentials file.
func DeleteCredentials() error {
	path := credentialsPath()
	err := os.Remove(path)
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		return fmt.Errorf("remove credentials: %w", err)
	}
	return nil
}

// IsLoggedIn checks if valid credentials exist without printing warnings.
func IsLoggedIn() (string, bool) {
	path := credentialsPath()
	data, err := os.ReadFile(path)
	if err != nil {
		return "", false
	}
	var creds Credentials
	if err := json.Unmarshal(data, &creds); err != nil {
		return "", false
	}
	if time.Now().After(creds.ExpiresAt) {
		return "", false
	}
	return creds.Username, true
}
