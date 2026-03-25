package main

import (
	"context"
	"fmt"
	"log"
	"net/http"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"github.com/ashmaster/promptrail/server/config"
	"github.com/ashmaster/promptrail/server/handlers"
	"github.com/ashmaster/promptrail/server/middleware"
	"github.com/ashmaster/promptrail/server/storage"
	"github.com/go-chi/chi/v5"
	chimw "github.com/go-chi/chi/v5/middleware"
	"github.com/golang-jwt/jwt/v5"
)

func main() {
	// Load .env for local dev (ignore error if not present)
	loadDotEnv()

	cfg, err := config.Load()
	if err != nil {
		log.Fatalf("config: %v", err)
	}

	ctx := context.Background()

	// Connect to database
	db, err := storage.NewDB(ctx, cfg.DatabaseURL)
	if err != nil {
		log.Fatalf("database: %v", err)
	}
	defer db.Close()

	// Run migrations
	if err := db.Migrate(ctx); err != nil {
		log.Fatalf("migrate: %v", err)
	}

	// Set up R2 storage (optional — presigned URLs won't work without it)
	var r2 *storage.R2Storage
	if cfg.R2AccountID != "" {
		var err error
		r2, err = storage.NewR2Storage(cfg.R2AccountID, cfg.R2AccessKeyID, cfg.R2SecretAccessKey, cfg.R2BucketName)
		if err != nil {
			log.Printf("warning: R2 not configured: %v", err)
		}
	}

	// Set up handlers
	authHandler := &handlers.AuthHandler{
		Config: cfg,
		DB:     db,
	}
	sessionHandler := &handlers.SessionHandler{
		Config: cfg,
		DB:     db,
		R2:     r2,
	}
	shareHandler := &handlers.ShareHandler{
		Config: cfg,
		DB:     db,
		R2:     r2,
	}

	// Set up router
	r := chi.NewRouter()
	r.Use(chimw.Logger)
	r.Use(chimw.Recoverer)
	r.Use(chimw.RealIP)

	// Health check
	r.Get("/health", func(w http.ResponseWriter, r *http.Request) {
		handlers.JSON(w, http.StatusOK, map[string]string{"status": "ok"})
	})

	// Auth routes (no JWT required)
	r.Get("/auth/github", authHandler.GitHubStart)
	r.Get("/auth/github/callback", authHandler.GitHubCallback)

	// Protected API routes
	r.Route("/api", func(r chi.Router) {
		r.Use(middleware.JWTAuth(cfg.JWTSecret))

		r.Get("/me", func(w http.ResponseWriter, r *http.Request) {
			handlers.JSON(w, http.StatusOK, map[string]string{
				"user_id":  middleware.UserIDFromCtx(r.Context()),
				"username": middleware.UsernameFromCtx(r.Context()),
			})
		})

		// Session CRUD
		r.Post("/sessions", sessionHandler.CreateSession)
		r.Get("/sessions", sessionHandler.ListSessions)
		r.Get("/sessions/{id}", sessionHandler.GetSession)
		r.Patch("/sessions/{id}", sessionHandler.UpdateAccess)
		r.Delete("/sessions/{id}", sessionHandler.DeleteSession)
		r.Put("/sessions/{id}/blob-uploaded", sessionHandler.ConfirmUpload)
	})

	// Session view by username/sessionId — supports both public (no auth) and private (auth required)
	// Uses optional auth: extracts user from JWT if present, but doesn't reject if missing
	r.Group(func(r chi.Router) {
		r.Use(optionalJWTAuth(cfg.JWTSecret))
		r.Get("/api/u/{username}/{sessionId}", shareHandler.GetByUserAndSession)
	})

	// Start server
	addr := fmt.Sprintf(":%d", cfg.Port)
	srv := &http.Server{
		Addr:         addr,
		Handler:      r,
		ReadTimeout:  10 * time.Second,
		WriteTimeout: 30 * time.Second,
		IdleTimeout:  60 * time.Second,
	}

	// Graceful shutdown
	go func() {
		sigCh := make(chan os.Signal, 1)
		signal.Notify(sigCh, syscall.SIGINT, syscall.SIGTERM)
		<-sigCh

		log.Println("shutting down...")
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		srv.Shutdown(shutdownCtx)
	}()

	log.Printf("server listening on %s", addr)
	if err := srv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
		log.Fatalf("server: %v", err)
	}
}

// optionalJWTAuth extracts user from JWT if present, but allows unauthenticated requests through.
func optionalJWTAuth(secret []byte) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			authHeader := r.Header.Get("Authorization")
			if authHeader == "" {
				next.ServeHTTP(w, r)
				return
			}
			parts := strings.SplitN(authHeader, " ", 2)
			if len(parts) != 2 || parts[0] != "Bearer" {
				next.ServeHTTP(w, r)
				return
			}
			token, err := jwt.ParseWithClaims(parts[1], &middleware.Claims{}, func(t *jwt.Token) (any, error) {
				if _, ok := t.Method.(*jwt.SigningMethodHMAC); !ok {
					return nil, jwt.ErrSignatureInvalid
				}
				return secret, nil
			})
			if err != nil || !token.Valid {
				next.ServeHTTP(w, r)
				return
			}
			claims, ok := token.Claims.(*middleware.Claims)
			if !ok {
				next.ServeHTTP(w, r)
				return
			}
			ctx := context.WithValue(r.Context(), middleware.UserIDKey, claims.Subject)
			ctx = context.WithValue(ctx, middleware.UsernameKey, claims.Username)
			next.ServeHTTP(w, r.WithContext(ctx))
		})
	}
}

// loadDotEnv reads a .env file if present and sets environment variables.
// Simple implementation — no external dependency needed.
func loadDotEnv() {
	data, err := os.ReadFile(".env")
	if err != nil {
		return
	}
	for _, line := range splitLines(string(data)) {
		line = strings.TrimSpace(line)
		if line == "" || line[0] == '#' {
			continue
		}
		k, v, ok := strings.Cut(line, "=")
		if !ok {
			continue
		}
		k = strings.TrimSpace(k)
		v = strings.TrimSpace(v)
		// Don't override existing env vars
		if os.Getenv(k) == "" {
			os.Setenv(k, v)
		}
	}
}

func splitLines(s string) []string {
	var lines []string
	for len(s) > 0 {
		i := strings.IndexByte(s, '\n')
		if i < 0 {
			lines = append(lines, s)
			break
		}
		lines = append(lines, s[:i])
		s = s[i+1:]
	}
	return lines
}
