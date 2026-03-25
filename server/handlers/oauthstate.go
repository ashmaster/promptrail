package handlers

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"strings"
	"time"
)

type OAuthStatePayload struct {
	Port  int    `json:"port"`
	Nonce string `json:"nonce"`
	Exp   int64  `json:"exp"`
}

// EncodeOAuthState creates an HMAC-signed state parameter.
// Format: base64url(json(payload)).hex(hmac_sha256(base64url_part, secret))
func EncodeOAuthState(payload OAuthStatePayload, secret []byte) string {
	jsonBytes, _ := json.Marshal(payload)
	b64 := base64.RawURLEncoding.EncodeToString(jsonBytes)
	mac := hmac.New(sha256.New, secret)
	mac.Write([]byte(b64))
	sig := hex.EncodeToString(mac.Sum(nil))
	return b64 + "." + sig
}

// DecodeOAuthState verifies and decodes an HMAC-signed state parameter.
func DecodeOAuthState(state string, secret []byte) (*OAuthStatePayload, error) {
	parts := strings.SplitN(state, ".", 2)
	if len(parts) != 2 {
		return nil, errors.New("invalid state format")
	}
	b64, sig := parts[0], parts[1]

	// Verify HMAC (constant-time comparison)
	mac := hmac.New(sha256.New, secret)
	mac.Write([]byte(b64))
	expectedSig := hex.EncodeToString(mac.Sum(nil))
	if !hmac.Equal([]byte(sig), []byte(expectedSig)) {
		return nil, errors.New("invalid state signature")
	}

	// Decode payload
	jsonBytes, err := base64.RawURLEncoding.DecodeString(b64)
	if err != nil {
		return nil, errors.New("invalid state encoding")
	}
	var payload OAuthStatePayload
	if err := json.Unmarshal(jsonBytes, &payload); err != nil {
		return nil, errors.New("invalid state payload")
	}

	// Check expiry
	if time.Now().Unix() > payload.Exp {
		return nil, errors.New("state expired")
	}

	return &payload, nil
}
