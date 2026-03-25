package api

import (
	"bytes"
	"compress/gzip"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"time"
)

type Client struct {
	BaseURL    string
	Token      string
	HTTPClient *http.Client
}

func NewClient(baseURL, token string) *Client {
	return &Client{
		BaseURL: baseURL,
		Token:   token,
		HTTPClient: &http.Client{
			Timeout: 30 * time.Second,
		},
	}
}

type CreateSessionRequest struct {
	ClaudeSessionID string `json:"claude_session_id"`
	Title           string `json:"title"`
	ProjectPath     string `json:"project_path"`
	BlobSizeBytes   int64  `json:"blob_size_bytes"`
	MessageCount    int    `json:"message_count"`
	Metadata        any    `json:"metadata"`
}

type CreateSessionResponse struct {
	SessionID  string `json:"session_id"`
	ShareToken string `json:"share_token"`
	UploadURL  string `json:"upload_url"`
	IsReupload bool   `json:"is_reupload"`
}

type ConfirmUploadRequest struct {
	BlobSizeBytes int64 `json:"blob_size_bytes"`
	MessageCount  int   `json:"message_count"`
}

type ConfirmUploadResponse struct {
	ShareURL string `json:"share_url"`
}

type SessionListResponse struct {
	Sessions []SessionInfo `json:"sessions"`
}

type SessionInfo struct {
	ID              string          `json:"id"`
	ClaudeSessionID string          `json:"claude_session_id"`
	Title           string          `json:"title"`
	ProjectPath     string          `json:"project_path"`
	Access          string          `json:"access"`
	ShareToken      string          `json:"share_token"`
	MessageCount    int             `json:"message_count"`
	BlobSizeBytes   int64           `json:"blob_size_bytes"`
	UploadedAt      string          `json:"uploaded_at"`
	Metadata        json.RawMessage `json:"metadata"`
}

// CreateSession calls POST /api/sessions
func (c *Client) CreateSession(req *CreateSessionRequest) (*CreateSessionResponse, error) {
	var resp CreateSessionResponse
	if err := c.doJSON("POST", "/api/sessions", req, &resp); err != nil {
		return nil, err
	}
	return &resp, nil
}

// ConfirmUpload calls PUT /api/sessions/{id}/blob-uploaded
func (c *Client) ConfirmUpload(sessionID string, req *ConfirmUploadRequest) (*ConfirmUploadResponse, error) {
	var resp ConfirmUploadResponse
	if err := c.doJSON("PUT", fmt.Sprintf("/api/sessions/%s/blob-uploaded", sessionID), req, &resp); err != nil {
		return nil, err
	}
	return &resp, nil
}

// ListSessions calls GET /api/sessions
func (c *Client) ListSessions() (*SessionListResponse, error) {
	var resp SessionListResponse
	if err := c.doJSON("GET", "/api/sessions", nil, &resp); err != nil {
		return nil, err
	}
	return &resp, nil
}

type GetSessionResponse struct {
	Session SessionInfo `json:"session"`
	BlobURL string      `json:"blob_url"`
}

// GetSession calls GET /api/sessions/{id} (authed, owner only)
func (c *Client) GetSession(sessionID string) (*GetSessionResponse, error) {
	var resp GetSessionResponse
	if err := c.doJSON("GET", fmt.Sprintf("/api/sessions/%s", sessionID), nil, &resp); err != nil {
		return nil, err
	}
	return &resp, nil
}

// GetSessionByUser calls GET /api/u/{username}/{sessionId}
// Works for public sessions without auth, private sessions need auth.
func (c *Client) GetSessionByUser(username, sessionID string) (*GetSessionResponse, error) {
	var resp GetSessionResponse
	if err := c.doJSON("GET", fmt.Sprintf("/api/u/%s/%s", username, sessionID), nil, &resp); err != nil {
		return nil, err
	}
	return &resp, nil
}

// FetchBlob downloads and decompresses a gzipped blob from a presigned URL.
func (c *Client) FetchBlob(blobURL string) ([]byte, error) {
	resp, err := c.HTTPClient.Get(blobURL)
	if err != nil {
		return nil, fmt.Errorf("fetch blob: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != 200 {
		return nil, fmt.Errorf("fetch blob: HTTP %d", resp.StatusCode)
	}

	// Decompress gzip
	gz, err := gzip.NewReader(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("decompress blob: %w", err)
	}
	defer gz.Close()

	return io.ReadAll(io.LimitReader(gz, 100*1024*1024)) // 100MB max
}

// UpdateAccess calls PATCH /api/sessions/{id}
func (c *Client) UpdateAccess(sessionID, access string) error {
	return c.doJSON("PATCH", fmt.Sprintf("/api/sessions/%s", sessionID), map[string]string{"access": access}, nil)
}

// UploadBlob PUTs the gzipped blob directly to the presigned R2 URL.
func (c *Client) UploadBlob(uploadURL string, data []byte) error {
	req, err := http.NewRequest("PUT", uploadURL, bytes.NewReader(data))
	if err != nil {
		return fmt.Errorf("create upload request: %w", err)
	}
	req.Header.Set("Content-Type", "application/gzip")
	req.ContentLength = int64(len(data))

	resp, err := c.HTTPClient.Do(req)
	if err != nil {
		return fmt.Errorf("upload blob: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode >= 300 {
		body, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("upload failed (HTTP %d): %s", resp.StatusCode, string(body))
	}

	return nil
}

func (c *Client) doJSON(method, path string, body any, result any) error {
	var reqBody io.Reader
	if body != nil {
		data, err := json.Marshal(body)
		if err != nil {
			return fmt.Errorf("marshal request: %w", err)
		}
		reqBody = bytes.NewReader(data)
	}

	req, err := http.NewRequest(method, c.BaseURL+path, reqBody)
	if err != nil {
		return fmt.Errorf("create request: %w", err)
	}
	req.Header.Set("Authorization", "Bearer "+c.Token)
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}

	resp, err := c.HTTPClient.Do(req)
	if err != nil {
		return fmt.Errorf("request %s %s: %w", method, path, err)
	}
	defer resp.Body.Close()

	respBody, err := io.ReadAll(resp.Body)
	if err != nil {
		return fmt.Errorf("read response: %w", err)
	}

	if resp.StatusCode >= 400 {
		var errResp struct {
			Error string `json:"error"`
		}
		if json.Unmarshal(respBody, &errResp) == nil && errResp.Error != "" {
			return fmt.Errorf("%s", errResp.Error)
		}
		return fmt.Errorf("HTTP %d: %s", resp.StatusCode, string(respBody))
	}

	if result != nil {
		var envelope struct {
			Data json.RawMessage `json:"data"`
		}
		if err := json.Unmarshal(respBody, &envelope); err != nil {
			return fmt.Errorf("parse response: %w", err)
		}
		if err := json.Unmarshal(envelope.Data, result); err != nil {
			return fmt.Errorf("parse response data: %w", err)
		}
	}

	return nil
}
