package handlers

import (
	"log"
	"net/http"
	"time"

	"github.com/ashmaster/promptrail/server/config"
	"github.com/ashmaster/promptrail/server/middleware"
	"github.com/ashmaster/promptrail/server/storage"
	"github.com/go-chi/chi/v5"
)

type ShareHandler struct {
	Config *config.Config
	DB     *storage.DB
	R2     *storage.R2Storage
}

// GetByUserAndSession handles GET /api/u/{username}/{sessionId}
// Public sessions: anyone can view. Private sessions: only the owner.
func (h *ShareHandler) GetByUserAndSession(w http.ResponseWriter, r *http.Request) {
	username := chi.URLParam(r, "username")
	sessionIDPrefix := chi.URLParam(r, "sessionId")

	session, err := h.DB.GetSessionByUsernameAndID(r.Context(), username, sessionIDPrefix)
	if err != nil {
		log.Printf("ERROR get by username: %v", err)
		JSONError(w, http.StatusInternalServerError, "internal error")
		return
	}
	if session == nil {
		JSONError(w, http.StatusNotFound, "not found")
		return
	}

	// Access control
	if session.Access == "private" {
		// Only the owner can view private sessions
		reqUserID := middleware.UserIDFromCtx(r.Context())
		if reqUserID == "" || reqUserID != session.UserID {
			JSONError(w, http.StatusNotFound, "not found")
			return
		}
	}

	// Generate presigned read URL
	var blobURL string
	if h.R2 != nil {
		url, err := h.R2.GenerateReadURL(r.Context(), session.BlobKey, 1*time.Hour)
		if err == nil {
			blobURL = url
		}
	}

	JSON(w, http.StatusOK, map[string]any{
		"session": map[string]any{
			"id":               session.ID,
			"claude_session_id": session.ClaudeSessionID,
			"title":            session.Title,
			"project_path":     session.ProjectPath,
			"access":           session.Access,
			"message_count":    session.MessageCount,
			"metadata":         session.Metadata,
			"uploaded_at":      session.UploadedAt,
		},
		"blob_url": blobURL,
	})
}
