package middleware

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"

	"github.com/golang-jwt/jwt/v5"
)

type contextKey string

const (
	UserIDKey   contextKey = "user_id"
	UsernameKey contextKey = "username"
)

// Claims is the JWT claims structure (duplicated here to avoid import cycle with handlers).
type Claims struct {
	jwt.RegisteredClaims
	Username string `json:"username"`
}

func UserIDFromCtx(ctx context.Context) string {
	v, _ := ctx.Value(UserIDKey).(string)
	return v
}

func UsernameFromCtx(ctx context.Context) string {
	v, _ := ctx.Value(UsernameKey).(string)
	return v
}

// JWTAuth returns middleware that validates JWT tokens and injects user info into the context.
func JWTAuth(secret []byte) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			authHeader := r.Header.Get("Authorization")
			if authHeader == "" {
				jsonError(w, http.StatusUnauthorized, "missing authorization header")
				return
			}

			parts := strings.SplitN(authHeader, " ", 2)
			if len(parts) != 2 || parts[0] != "Bearer" {
				jsonError(w, http.StatusUnauthorized, "invalid authorization format")
				return
			}
			tokenString := parts[1]

			token, err := jwt.ParseWithClaims(tokenString, &Claims{}, func(t *jwt.Token) (any, error) {
				if _, ok := t.Method.(*jwt.SigningMethodHMAC); !ok {
					return nil, jwt.ErrSignatureInvalid
				}
				return secret, nil
			})
			if err != nil {
				if err == jwt.ErrTokenExpired {
					jsonError(w, http.StatusUnauthorized, "token expired")
					return
				}
				jsonError(w, http.StatusUnauthorized, "invalid token")
				return
			}

			claims, ok := token.Claims.(*Claims)
			if !ok || !token.Valid {
				jsonError(w, http.StatusUnauthorized, "invalid token")
				return
			}

			ctx := context.WithValue(r.Context(), UserIDKey, claims.Subject)
			ctx = context.WithValue(ctx, UsernameKey, claims.Username)
			next.ServeHTTP(w, r.WithContext(ctx))
		})
	}
}

func jsonError(w http.ResponseWriter, status int, message string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	json.NewEncoder(w).Encode(map[string]string{"error": message})
}
