package gateway

import (
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
)

var randReader io.Reader = rand.Reader

// AuthMiddleware checks the Authorization header for a Bearer token.
func AuthMiddleware(token string) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			// Health endpoint is always accessible
			if r.URL.Path == "/api/v1/health" {
				next.ServeHTTP(w, r)
				return
			}

			// Check Authorization header
			auth := r.Header.Get("Authorization")
			if auth == "" && (r.URL.Path == "/ws" || r.URL.Path == "/") {
				// Browser WebSocket clients cannot set arbitrary headers in the
				// opening handshake, and the webchat root page uses query token
				// bootstrap before JavaScript can attach Authorization headers.
				if q := strings.TrimSpace(r.URL.Query().Get("token")); q != "" {
					auth = "Bearer " + q
				}
			}

			if !strings.HasPrefix(auth, "Bearer ") {
				http.Error(w, `{"error":"missing authorization header"}`, http.StatusUnauthorized)
				return
			}

			provided := strings.TrimPrefix(auth, "Bearer ")
			if provided != token {
				http.Error(w, `{"error":"invalid token"}`, http.StatusUnauthorized)
				return
			}

			next.ServeHTTP(w, r)
		})
	}
}

// GenerateToken creates a cryptographically random auth token.
func GenerateToken() (string, error) {
	b := make([]byte, 32)
	if _, err := io.ReadFull(randReader, b); err != nil {
		return "", fmt.Errorf("reading crypto random bytes: %w", err)
	}
	return hex.EncodeToString(b), nil
}

// LoadOrCreateToken loads the auth token from disk, or creates one.
func LoadOrCreateToken(dataDir string) (string, error) {
	tokenPath := filepath.Join(dataDir, "auth.token")

	data, err := os.ReadFile(tokenPath)
	if err == nil {
		token := strings.TrimSpace(string(data))
		if token != "" {
			return token, nil
		}
	}

	token, err := GenerateToken()
	if err != nil {
		return "", fmt.Errorf("generating auth token: %w", err)
	}

	if err := os.MkdirAll(dataDir, 0700); err != nil {
		return "", fmt.Errorf("creating data directory: %w", err)
	}

	if err := os.WriteFile(tokenPath, []byte(token+"\n"), 0600); err != nil {
		return "", fmt.Errorf("writing auth token: %w", err)
	}

	return token, nil
}

// RotateToken generates a new auth token and overwrites the existing one.
func RotateToken(dataDir string) (string, error) {
	tokenPath := filepath.Join(dataDir, "auth.token")
	token, err := GenerateToken()
	if err != nil {
		return "", fmt.Errorf("generating auth token: %w", err)
	}
	if err := os.WriteFile(tokenPath, []byte(token+"\n"), 0600); err != nil {
		return "", fmt.Errorf("rotating auth token: %w", err)
	}
	return token, nil
}
