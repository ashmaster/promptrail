package handlers

import (
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"time"

	"github.com/ashmaster/promptrail/server/config"
	"github.com/ashmaster/promptrail/server/middleware"
	"github.com/ashmaster/promptrail/server/storage"
	"github.com/go-chi/chi/v5"
)

type SessionHandler struct {
	Config *config.Config
	DB     *storage.DB
	R2     *storage.R2Storage
}

type createSessionRequest struct {
	ClaudeSessionID string          `json:"claude_session_id"`
	Title           string          `json:"title"`
	ProjectPath     string          `json:"project_path"`
	BlobSizeBytes   int64           `json:"blob_size_bytes"`
	MessageCount    int             `json:"message_count"`
	Metadata        json.RawMessage `json:"metadata"`
}

type confirmUploadRequest struct {
	BlobSizeBytes int64 `json:"blob_size_bytes"`
	MessageCount  int   `json:"message_count"`
}

type updateAccessRequest struct {
	Access string `json:"access"`
}

// CreateSession handles POST /api/sessions
func (h *SessionHandler) CreateSession(w http.ResponseWriter, r *http.Request) {
	userID := middleware.UserIDFromCtx(r.Context())

	r.Body = http.MaxBytesReader(w, r.Body, 1<<20) // 1MB limit
	var req createSessionRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		JSONError(w, http.StatusBadRequest, "invalid request body")
		return
	}

	if req.ClaudeSessionID == "" || req.Title == "" {
		JSONError(w, http.StatusBadRequest, "claude_session_id and title are required")
		return
	}

	blobKey := storage.BlobKey(userID, req.ClaudeSessionID)

	session, isReupload, err := h.DB.CreateOrUpdateSession(
		r.Context(), userID, req.ClaudeSessionID,
		req.Title, req.ProjectPath, blobKey, req.Metadata,
	)
	if err != nil {
		log.Printf("ERROR create session: %v", err)
		JSONError(w, http.StatusInternalServerError, "failed to create session")
		return
	}

	// Generate presigned upload URL
	uploadURL := ""
	if h.R2 != nil {
		url, err := h.R2.GenerateUploadURL(r.Context(), session.BlobKey, 15*time.Minute)
		if err != nil {
			log.Printf("ERROR generate upload URL: %v", err)
			JSONError(w, http.StatusInternalServerError, "failed to generate upload URL")
			return
		}
		uploadURL = url
	}

	status := http.StatusCreated
	if isReupload {
		status = http.StatusOK
	}

	JSON(w, status, map[string]any{
		"session_id":  session.ID,
		"share_token": session.ShareToken,
		"upload_url":  uploadURL,
		"is_reupload": isReupload,
	})
}

// ConfirmUpload handles PUT /api/sessions/{id}/blob-uploaded
func (h *SessionHandler) ConfirmUpload(w http.ResponseWriter, r *http.Request) {
	userID := middleware.UserIDFromCtx(r.Context())
	sessionID := chi.URLParam(r, "id")

	r.Body = http.MaxBytesReader(w, r.Body, 1<<20)
	var req confirmUploadRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		JSONError(w, http.StatusBadRequest, "invalid request body")
		return
	}

	if err := h.DB.ConfirmUpload(r.Context(), sessionID, userID, req.BlobSizeBytes, req.MessageCount); err != nil {
		JSONError(w, http.StatusNotFound, "session not found")
		return
	}

	// Get updated session for share URL
	session, err := h.DB.GetSession(r.Context(), sessionID, userID)
	if err != nil || session == nil {
		JSONError(w, http.StatusInternalServerError, "failed to get session")
		return
	}

	shareURL := fmt.Sprintf("%s/s/%s", h.Config.BaseURL, session.ShareToken)

	JSON(w, http.StatusOK, map[string]any{
		"share_url": shareURL,
	})
}

// ListSessions handles GET /api/sessions
func (h *SessionHandler) ListSessions(w http.ResponseWriter, r *http.Request) {
	userID := middleware.UserIDFromCtx(r.Context())

	sessions, err := h.DB.ListSessions(r.Context(), userID)
	if err != nil {
		JSONError(w, http.StatusInternalServerError, "failed to list sessions")
		return
	}

	JSON(w, http.StatusOK, map[string]any{
		"sessions": sessions,
	})
}

// GetSession handles GET /api/sessions/{id}
func (h *SessionHandler) GetSession(w http.ResponseWriter, r *http.Request) {
	userID := middleware.UserIDFromCtx(r.Context())
	sessionID := chi.URLParam(r, "id")

	session, err := h.DB.GetSession(r.Context(), sessionID, userID)
	if err != nil {
		JSONError(w, http.StatusInternalServerError, "failed to get session")
		return
	}
	if session == nil {
		JSONError(w, http.StatusNotFound, "session not found")
		return
	}

	// Generate presigned read URL for the blob
	var blobURL string
	if h.R2 != nil {
		url, err := h.R2.GenerateReadURL(r.Context(), session.BlobKey, 1*time.Hour)
		if err == nil {
			blobURL = url
		}
	}

	JSON(w, http.StatusOK, map[string]any{
		"session":  session,
		"blob_url": blobURL,
	})
}

// UpdateAccess handles PATCH /api/sessions/{id}
func (h *SessionHandler) UpdateAccess(w http.ResponseWriter, r *http.Request) {
	userID := middleware.UserIDFromCtx(r.Context())
	sessionID := chi.URLParam(r, "id")

	r.Body = http.MaxBytesReader(w, r.Body, 1<<20)
	var req updateAccessRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		JSONError(w, http.StatusBadRequest, "invalid request body")
		return
	}

	if req.Access != "private" && req.Access != "public" {
		JSONError(w, http.StatusBadRequest, "access must be private or public")
		return
	}

	if err := h.DB.UpdateAccess(r.Context(), sessionID, userID, req.Access); err != nil {
		JSONError(w, http.StatusNotFound, "session not found")
		return
	}

	JSON(w, http.StatusOK, map[string]string{"status": "updated"})
}

// DeleteSession handles DELETE /api/sessions/{id}
func (h *SessionHandler) DeleteSession(w http.ResponseWriter, r *http.Request) {
	userID := middleware.UserIDFromCtx(r.Context())
	sessionID := chi.URLParam(r, "id")

	blobKey, err := h.DB.DeleteSession(r.Context(), sessionID, userID)
	if err != nil {
		JSONError(w, http.StatusNotFound, "session not found")
		return
	}

	// Delete blob from R2
	if h.R2 != nil && blobKey != "" {
		h.R2.DeleteObject(r.Context(), blobKey)
	}

	JSON(w, http.StatusOK, map[string]string{"status": "deleted"})
}
