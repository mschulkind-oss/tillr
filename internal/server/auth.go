package server

import (
	"crypto/sha256"
	"crypto/subtle"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"net/http"
	"strings"

	"github.com/mschulkind-oss/tillr/internal/db"
)

// AuthMiddleware returns an http.Handler that requires a valid API key for
// /api/* routes, except /api/docs and /api/health which remain public.
// Static files and WebSocket connections are not affected.
// It checks the config API key first, then falls back to DB-backed tokens.
func AuthMiddleware(apiKey string, next http.Handler) http.Handler {
	keyBytes := []byte(apiKey)
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !strings.HasPrefix(r.URL.Path, "/api/") || r.URL.Path == "/api/docs" || r.URL.Path == "/api/health" {
			next.ServeHTTP(w, r)
			return
		}

		// Allow CORS preflight through without auth
		if r.Method == http.MethodOptions {
			next.ServeHTTP(w, r)
			return
		}

		provided := extractAPIKey(r)
		if provided == "" {
			unauthorizedResponse(w)
			return
		}

		// Check against config API key
		if subtle.ConstantTimeCompare([]byte(provided), keyBytes) == 1 {
			next.ServeHTTP(w, r)
			return
		}

		unauthorizedResponse(w)
	})
}

// AuthMiddlewareWithDB returns an http.Handler that supports both config API key
// and DB-backed API tokens. DB tokens are validated by hashing the provided token
// and looking up the hash.
func AuthMiddlewareWithDB(apiKey string, database *sql.DB, next http.Handler) http.Handler {
	keyBytes := []byte(apiKey)
	hasConfigKey := apiKey != ""

	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !strings.HasPrefix(r.URL.Path, "/api/") || r.URL.Path == "/api/docs" || r.URL.Path == "/api/health" {
			next.ServeHTTP(w, r)
			return
		}

		// Allow CORS preflight through without auth
		if r.Method == http.MethodOptions {
			next.ServeHTTP(w, r)
			return
		}

		provided := extractAPIKey(r)
		if provided == "" {
			unauthorizedResponse(w)
			return
		}

		// Check against config API key first
		if hasConfigKey && subtle.ConstantTimeCompare([]byte(provided), keyBytes) == 1 {
			next.ServeHTTP(w, r)
			return
		}

		// Check DB-backed tokens
		tokenHash := hashTokenForAuth(provided)
		if _, err := db.GetAPITokenByHash(database, tokenHash); err == nil {
			next.ServeHTTP(w, r)
			return
		}

		unauthorizedResponse(w)
	})
}

func unauthorizedResponse(w http.ResponseWriter) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusUnauthorized)
	json.NewEncoder(w).Encode(map[string]string{ //nolint:errcheck
		"error": "unauthorized: valid API key required",
	})
}

func hashTokenForAuth(token string) string {
	h := sha256.Sum256([]byte(token))
	return hex.EncodeToString(h[:])
}

// extractAPIKey extracts the API key from the Authorization header (Bearer token)
// or the api_key query parameter.
func extractAPIKey(r *http.Request) string {
	if auth := r.Header.Get("Authorization"); auth != "" {
		if after, ok := strings.CutPrefix(auth, "Bearer "); ok {
			return strings.TrimSpace(after)
		}
	}
	return r.URL.Query().Get("api_key")
}
