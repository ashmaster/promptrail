package handlers

import (
	"encoding/json"
	"fmt"
	"html"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"time"

	"github.com/ashmaster/promptrail/server/config"
	"github.com/ashmaster/promptrail/server/storage"
	"github.com/golang-jwt/jwt/v5"
)

var githubHTTPClient = &http.Client{Timeout: 10 * time.Second}

type AuthHandler struct {
	Config *config.Config
	DB     *storage.DB
}

type Claims struct {
	jwt.RegisteredClaims
	Username string `json:"username"`
}

// GitHubStart handles GET /auth/github
// Validates CLI params, builds signed state, redirects to GitHub OAuth.
func (h *AuthHandler) GitHubStart(w http.ResponseWriter, r *http.Request) {
	nonce := r.URL.Query().Get("nonce")
	portStr := r.URL.Query().Get("callback_port")

	// Validate nonce
	if len(nonce) != 64 {
		http.Error(w, "nonce must be 64-char hex string", http.StatusBadRequest)
		return
	}

	// Validate callback_port
	port, err := strconv.Atoi(portStr)
	if err != nil || port < 1024 || port > 65535 {
		http.Error(w, "callback_port must be integer 1024-65535", http.StatusBadRequest)
		return
	}

	// Build signed state (HMAC-signed, stateless)
	payload := OAuthStatePayload{
		Port:  port,
		Nonce: nonce,
		Exp:   time.Now().Add(5 * time.Minute).Unix(),
	}
	state := EncodeOAuthState(payload, h.Config.JWTSecret)

	// Redirect to GitHub
	ghURL := fmt.Sprintf(
		"https://github.com/login/oauth/authorize?client_id=%s&redirect_uri=%s&scope=read:user&state=%s",
		url.QueryEscape(h.Config.GitHubClientID),
		url.QueryEscape(h.Config.BaseURL+"/auth/github/callback"),
		url.QueryEscape(state),
	)

	http.Redirect(w, r, ghURL, http.StatusFound)
}

// GitHubCallback handles GET /auth/github/callback
// Verifies state, exchanges code, upserts user, issues JWT, redirects to CLI.
func (h *AuthHandler) GitHubCallback(w http.ResponseWriter, r *http.Request) {
	state := r.URL.Query().Get("state")
	code := r.URL.Query().Get("code")
	oauthError := r.URL.Query().Get("error")

	// Decode and verify signed state
	payload, err := DecodeOAuthState(state, h.Config.JWTSecret)
	if err != nil {
		renderErrorPage(w, "Login session expired or invalid. Please try again.", http.StatusBadRequest)
		return
	}

	callbackBase := fmt.Sprintf("http://127.0.0.1:%d/callback", payload.Port)

	// Handle user denial
	if oauthError != "" {
		redirectURL := fmt.Sprintf("%s?error=%s&nonce=%s", callbackBase, url.QueryEscape(oauthError), url.QueryEscape(payload.Nonce))
		http.Redirect(w, r, redirectURL, http.StatusFound)
		return
	}

	if code == "" {
		renderErrorPage(w, "Missing authorization code.", http.StatusBadRequest)
		return
	}

	// Exchange code for GitHub access token
	ghToken, err := h.exchangeCode(code)
	if err != nil {
		renderErrorPage(w, "Failed to authenticate with GitHub. Please try again.", http.StatusInternalServerError)
		return
	}

	// Fetch GitHub user profile
	ghUser, err := h.fetchGitHubUser(ghToken)
	if err != nil {
		renderErrorPage(w, "Failed to fetch GitHub profile. Please try again.", http.StatusInternalServerError)
		return
	}

	// Upsert user in DB
	user, err := h.DB.UpsertUser(r.Context(), ghUser.ID, ghUser.Login, ghUser.AvatarURL)
	if err != nil {
		renderErrorPage(w, "Internal server error. Please try again.", http.StatusInternalServerError)
		return
	}

	// Generate JWT
	token, err := h.generateJWT(user)
	if err != nil {
		renderErrorPage(w, "Internal server error. Please try again.", http.StatusInternalServerError)
		return
	}

	// Redirect to CLI callback
	redirectURL := fmt.Sprintf("%s?token=%s&nonce=%s&username=%s",
		callbackBase,
		url.QueryEscape(token),
		url.QueryEscape(payload.Nonce),
		url.QueryEscape(user.GitHubUsername),
	)
	http.Redirect(w, r, redirectURL, http.StatusFound)
}

type githubTokenResponse struct {
	AccessToken string `json:"access_token"`
	TokenType   string `json:"token_type"`
	Scope       string `json:"scope"`
	Error       string `json:"error"`
}

func (h *AuthHandler) exchangeCode(code string) (string, error) {
	data := url.Values{
		"client_id":     {h.Config.GitHubClientID},
		"client_secret": {h.Config.GitHubClientSecret},
		"code":          {code},
		"redirect_uri":  {h.Config.BaseURL + "/auth/github/callback"},
	}

	req, err := http.NewRequest("POST", "https://github.com/login/oauth/access_token", nil)
	if err != nil {
		return "", err
	}
	req.URL.RawQuery = data.Encode()
	req.Header.Set("Accept", "application/json")

	resp, err := githubHTTPClient.Do(req)
	if err != nil {
		return "", fmt.Errorf("github token request: %w", err)
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return "", fmt.Errorf("read github response: %w", err)
	}

	var tokenResp githubTokenResponse
	if err := json.Unmarshal(body, &tokenResp); err != nil {
		return "", fmt.Errorf("parse github response: %w", err)
	}

	if tokenResp.Error != "" {
		return "", fmt.Errorf("github oauth error: %s", tokenResp.Error)
	}

	if tokenResp.AccessToken == "" {
		return "", fmt.Errorf("empty access token from github")
	}

	return tokenResp.AccessToken, nil
}

type githubUser struct {
	ID        int64  `json:"id"`
	Login     string `json:"login"`
	AvatarURL string `json:"avatar_url"`
}

func (h *AuthHandler) fetchGitHubUser(accessToken string) (*githubUser, error) {
	req, err := http.NewRequest("GET", "https://api.github.com/user", nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Authorization", "Bearer "+accessToken)
	req.Header.Set("Accept", "application/json")

	resp, err := githubHTTPClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("github user request: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("github user API returned %d", resp.StatusCode)
	}

	var user githubUser
	if err := json.NewDecoder(resp.Body).Decode(&user); err != nil {
		return nil, fmt.Errorf("parse github user: %w", err)
	}

	return &user, nil
}

func (h *AuthHandler) generateJWT(user *storage.User) (string, error) {
	now := time.Now()
	claims := Claims{
		RegisteredClaims: jwt.RegisteredClaims{
			Subject:   user.ID,
			IssuedAt:  jwt.NewNumericDate(now),
			ExpiresAt: jwt.NewNumericDate(now.Add(30 * 24 * time.Hour)),
		},
		Username: user.GitHubUsername,
	}

	token := jwt.NewWithClaims(jwt.SigningMethodHS256, claims)
	return token.SignedString(h.Config.JWTSecret)
}

func renderErrorPage(w http.ResponseWriter, message string, status int) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.WriteHeader(status)
	fmt.Fprintf(w, `<!DOCTYPE html><html><body><h2>Login Failed</h2><p>%s</p></body></html>`, html.EscapeString(message))
}
