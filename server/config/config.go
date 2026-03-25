package config

import (
	"fmt"
	"os"
	"strconv"
)

type Config struct {
	Port               int
	DatabaseURL        string
	JWTSecret          []byte
	GitHubClientID     string
	GitHubClientSecret string
	R2AccountID        string
	R2AccessKeyID      string
	R2SecretAccessKey  string
	R2BucketName       string
	BaseURL            string
}

func Load() (*Config, error) {
	port := 8080
	if v := os.Getenv("PORT"); v != "" {
		p, err := strconv.Atoi(v)
		if err != nil {
			return nil, fmt.Errorf("invalid PORT: %w", err)
		}
		port = p
	}

	jwtSecret := os.Getenv("JWT_SECRET")
	if len(jwtSecret) < 32 {
		return nil, fmt.Errorf("JWT_SECRET must be at least 32 bytes, got %d", len(jwtSecret))
	}

	cfg := &Config{
		Port:               port,
		DatabaseURL:        requireEnv("DATABASE_URL"),
		JWTSecret:          []byte(jwtSecret),
		GitHubClientID:     requireEnv("GITHUB_CLIENT_ID"),
		GitHubClientSecret: requireEnv("GITHUB_CLIENT_SECRET"),
		R2AccountID:        os.Getenv("R2_ACCOUNT_ID"),
		R2AccessKeyID:      os.Getenv("R2_ACCESS_KEY_ID"),
		R2SecretAccessKey:  os.Getenv("R2_SECRET_ACCESS_KEY"),
		R2BucketName:       envOr("R2_BUCKET_NAME", "csa-sessions"),
		BaseURL:            envOr("BASE_URL", "http://localhost:8080"),
	}

	if cfg.DatabaseURL == "" || cfg.GitHubClientID == "" || cfg.GitHubClientSecret == "" {
		return nil, fmt.Errorf("DATABASE_URL, GITHUB_CLIENT_ID, and GITHUB_CLIENT_SECRET are required")
	}

	return cfg, nil
}

func requireEnv(key string) string {
	return os.Getenv(key)
}

func envOr(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return fallback
}
