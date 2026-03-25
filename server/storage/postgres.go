package storage

import (
	"context"
	"crypto/rand"
	"encoding/json"
	"fmt"
	"math/big"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

type DB struct {
	Pool *pgxpool.Pool
}

type User struct {
	ID             string
	GitHubID       int64
	GitHubUsername string
	AvatarURL      string
}

func NewDB(ctx context.Context, databaseURL string) (*DB, error) {
	pool, err := pgxpool.New(ctx, databaseURL)
	if err != nil {
		return nil, fmt.Errorf("connect to database: %w", err)
	}
	if err := pool.Ping(ctx); err != nil {
		pool.Close()
		return nil, fmt.Errorf("ping database: %w", err)
	}
	return &DB{Pool: pool}, nil
}

func (db *DB) Close() {
	db.Pool.Close()
}

// UpsertUser creates or updates a user from GitHub OAuth data.
// Returns the internal user ID.
func (db *DB) UpsertUser(ctx context.Context, githubID int64, username, avatarURL string) (*User, error) {
	var user User
	err := db.Pool.QueryRow(ctx, `
		INSERT INTO users (github_id, github_username, avatar_url)
		VALUES ($1, $2, $3)
		ON CONFLICT (github_id)
		DO UPDATE SET github_username = $2, avatar_url = $3
		RETURNING id, github_id, github_username, avatar_url
	`, githubID, username, avatarURL).Scan(&user.ID, &user.GitHubID, &user.GitHubUsername, &user.AvatarURL)
	if err != nil {
		return nil, fmt.Errorf("upsert user: %w", err)
	}
	return &user, nil
}

// Session types

type Session struct {
	ID              string          `json:"id"`
	UserID          string          `json:"user_id"`
	ClaudeSessionID string          `json:"claude_session_id"`
	Title           string          `json:"title"`
	ProjectPath     *string         `json:"project_path"`
	Access          string          `json:"access"`
	ShareToken      string          `json:"share_token"`
	BlobKey         string          `json:"blob_key"`
	BlobSizeBytes   *int64          `json:"blob_size_bytes"`
	MessageCount    *int            `json:"message_count"`
	Metadata        json.RawMessage `json:"metadata"`
	CreatedAt       time.Time       `json:"created_at"`
	UploadedAt      time.Time       `json:"uploaded_at"`
}

// CreateOrUpdateSession creates a new session or returns the existing one for re-upload.
func (db *DB) CreateOrUpdateSession(ctx context.Context, userID, claudeSessionID, title, projectPath, blobKey string, metadata json.RawMessage) (*Session, bool, error) {
	// Check if session exists
	var session Session
	err := db.Pool.QueryRow(ctx, `
		SELECT id, user_id, claude_session_id, title, project_path, access, share_token,
			   blob_key, blob_size_bytes, message_count, metadata, created_at, uploaded_at
		FROM sessions
		WHERE user_id = $1 AND claude_session_id = $2
	`, userID, claudeSessionID).Scan(
		&session.ID, &session.UserID, &session.ClaudeSessionID, &session.Title,
		&session.ProjectPath, &session.Access, &session.ShareToken,
		&session.BlobKey, &session.BlobSizeBytes, &session.MessageCount,
		&session.Metadata, &session.CreatedAt, &session.UploadedAt,
	)

	if err == nil {
		// Existing session — update metadata for re-upload
		_, err = db.Pool.Exec(ctx, `
			UPDATE sessions SET title = $1, project_path = $2, metadata = $3 WHERE id = $4
		`, title, projectPath, metadata, session.ID)
		if err != nil {
			return nil, false, fmt.Errorf("update session: %w", err)
		}
		session.Title = title
		session.ProjectPath = &projectPath
		session.Metadata = metadata
		return &session, true, nil // isReupload = true
	}

	if err != pgx.ErrNoRows {
		return nil, false, fmt.Errorf("query session: %w", err)
	}

	// New session
	shareToken := generateShareToken()
	err = db.Pool.QueryRow(ctx, `
		INSERT INTO sessions (user_id, claude_session_id, title, project_path, access, share_token, blob_key, metadata)
		VALUES ($1, $2, $3, $4, 'private', $5, $6, $7)
		RETURNING id, user_id, claude_session_id, title, project_path, access, share_token,
				  blob_key, blob_size_bytes, message_count, metadata, created_at, uploaded_at
	`, userID, claudeSessionID, title, projectPath, shareToken, blobKey, metadata).Scan(
		&session.ID, &session.UserID, &session.ClaudeSessionID, &session.Title,
		&session.ProjectPath, &session.Access, &session.ShareToken,
		&session.BlobKey, &session.BlobSizeBytes, &session.MessageCount,
		&session.Metadata, &session.CreatedAt, &session.UploadedAt,
	)
	if err != nil {
		return nil, false, fmt.Errorf("insert session: %w", err)
	}

	return &session, false, nil
}

// ConfirmUpload updates session after blob upload is confirmed.
func (db *DB) ConfirmUpload(ctx context.Context, sessionID, userID string, blobSizeBytes int64, messageCount int) error {
	tag, err := db.Pool.Exec(ctx, `
		UPDATE sessions SET blob_size_bytes = $1, message_count = $2, uploaded_at = now()
		WHERE id = $3 AND user_id = $4
	`, blobSizeBytes, messageCount, sessionID, userID)
	if err != nil {
		return fmt.Errorf("confirm upload: %w", err)
	}
	if tag.RowsAffected() == 0 {
		return fmt.Errorf("session not found")
	}
	return nil
}

// GetSession returns a session by ID, scoped to the user.
func (db *DB) GetSession(ctx context.Context, sessionID, userID string) (*Session, error) {
	var s Session
	err := db.Pool.QueryRow(ctx, `
		SELECT id, user_id, claude_session_id, title, project_path, access, share_token,
			   blob_key, blob_size_bytes, message_count, metadata, created_at, uploaded_at
		FROM sessions WHERE id = $1 AND user_id = $2
	`, sessionID, userID).Scan(
		&s.ID, &s.UserID, &s.ClaudeSessionID, &s.Title,
		&s.ProjectPath, &s.Access, &s.ShareToken,
		&s.BlobKey, &s.BlobSizeBytes, &s.MessageCount,
		&s.Metadata, &s.CreatedAt, &s.UploadedAt,
	)
	if err != nil {
		if err == pgx.ErrNoRows {
			return nil, nil
		}
		return nil, fmt.Errorf("get session: %w", err)
	}
	return &s, nil
}

// ListSessions returns all sessions for a user.
func (db *DB) ListSessions(ctx context.Context, userID string) ([]Session, error) {
	rows, err := db.Pool.Query(ctx, `
		SELECT id, user_id, claude_session_id, title, project_path, access, share_token,
			   blob_key, blob_size_bytes, message_count, metadata, created_at, uploaded_at
		FROM sessions WHERE user_id = $1
		ORDER BY uploaded_at DESC
	`, userID)
	if err != nil {
		return nil, fmt.Errorf("list sessions: %w", err)
	}
	defer rows.Close()

	var sessions []Session
	for rows.Next() {
		var s Session
		if err := rows.Scan(
			&s.ID, &s.UserID, &s.ClaudeSessionID, &s.Title,
			&s.ProjectPath, &s.Access, &s.ShareToken,
			&s.BlobKey, &s.BlobSizeBytes, &s.MessageCount,
			&s.Metadata, &s.CreatedAt, &s.UploadedAt,
		); err != nil {
			return nil, fmt.Errorf("scan session: %w", err)
		}
		sessions = append(sessions, s)
	}
	return sessions, nil
}

// UpdateAccess changes the access level of a session.
func (db *DB) UpdateAccess(ctx context.Context, sessionID, userID, access string) error {
	tag, err := db.Pool.Exec(ctx, `
		UPDATE sessions SET access = $1 WHERE id = $2 AND user_id = $3
	`, access, sessionID, userID)
	if err != nil {
		return fmt.Errorf("update access: %w", err)
	}
	if tag.RowsAffected() == 0 {
		return fmt.Errorf("session not found")
	}
	return nil
}

// DeleteSession removes a session.
func (db *DB) DeleteSession(ctx context.Context, sessionID, userID string) (string, error) {
	var blobKey string
	err := db.Pool.QueryRow(ctx, `
		DELETE FROM sessions WHERE id = $1 AND user_id = $2 RETURNING blob_key
	`, sessionID, userID).Scan(&blobKey)
	if err != nil {
		if err == pgx.ErrNoRows {
			return "", fmt.Errorf("session not found")
		}
		return "", fmt.Errorf("delete session: %w", err)
	}
	return blobKey, nil
}

// GetSessionByUsernameAndID looks up a session by GitHub username + claude_session_id prefix.
// Returns the session and the owner's username.
func (db *DB) GetSessionByUsernameAndID(ctx context.Context, username, sessionIDPrefix string) (*Session, error) {
	var s Session
	err := db.Pool.QueryRow(ctx, `
		SELECT s.id, s.user_id, s.claude_session_id, s.title, s.project_path, s.access, s.share_token,
			   s.blob_key, s.blob_size_bytes, s.message_count, s.metadata, s.created_at, s.uploaded_at
		FROM sessions s
		JOIN users u ON s.user_id = u.id
		WHERE u.github_username = $1 AND s.claude_session_id LIKE replace(replace($2, '%', ''), '_', '') || '%'
		LIMIT 1
	`, username, sessionIDPrefix).Scan(
		&s.ID, &s.UserID, &s.ClaudeSessionID, &s.Title,
		&s.ProjectPath, &s.Access, &s.ShareToken,
		&s.BlobKey, &s.BlobSizeBytes, &s.MessageCount,
		&s.Metadata, &s.CreatedAt, &s.UploadedAt,
	)
	if err != nil {
		if err == pgx.ErrNoRows {
			return nil, nil
		}
		return nil, fmt.Errorf("get by username and id: %w", err)
	}
	return &s, nil
}

const shareTokenAlphabet = "0123456789ABCDEFGHIJKLMNOPQRSTUVWXYZabcdefghijklmnopqrstuvwxyz"

func generateShareToken() string {
	b := make([]byte, 8)
	for i := range b {
		n, _ := rand.Int(rand.Reader, big.NewInt(int64(len(shareTokenAlphabet))))
		b[i] = shareTokenAlphabet[n.Int64()]
	}
	return string(b)
}

// Migrate runs the schema migrations. For v1, just ensures tables exist.
func (db *DB) Migrate(ctx context.Context) error {
	_, err := db.Pool.Exec(ctx, `
		CREATE TABLE IF NOT EXISTS users (
			id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
			github_id BIGINT UNIQUE NOT NULL,
			github_username TEXT NOT NULL,
			avatar_url TEXT,
			created_at TIMESTAMPTZ DEFAULT now()
		);

		CREATE TABLE IF NOT EXISTS sessions (
			id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
			user_id UUID NOT NULL REFERENCES users(id),
			claude_session_id TEXT NOT NULL,
			title TEXT NOT NULL,
			project_path TEXT,
			access TEXT NOT NULL DEFAULT 'private' CHECK (access IN ('private', 'public')),
			share_token TEXT UNIQUE NOT NULL,
			blob_key TEXT NOT NULL,
			blob_size_bytes BIGINT,
			message_count INT,
			metadata JSONB,
			created_at TIMESTAMPTZ DEFAULT now(),
			uploaded_at TIMESTAMPTZ DEFAULT now(),
			UNIQUE (user_id, claude_session_id)
		);

		CREATE INDEX IF NOT EXISTS idx_sessions_user_id ON sessions(user_id);
		CREATE INDEX IF NOT EXISTS idx_sessions_share_token ON sessions(share_token);
	`)
	if err != nil {
		return fmt.Errorf("migrate: %w", err)
	}
	return nil
}
