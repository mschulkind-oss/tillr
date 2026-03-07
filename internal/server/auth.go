package server

import (
	"crypto/subtle"
	"encoding/json"
	"net/http"
	"strings"
)

// AuthMiddleware returns an http.Handler that requires a valid API key for
// /api/* routes, except /api/docs which remains public. Static files and
// WebSocket connections are not affected.
func AuthMiddleware(apiKey string, next http.Handler) http.Handler {
	keyBytes := []byte(apiKey)
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !strings.HasPrefix(r.URL.Path, "/api/") || r.URL.Path == "/api/docs" {
			next.ServeHTTP(w, r)
			return
		}

		// Allow CORS preflight through without auth
		if r.Method == http.MethodOptions {
			next.ServeHTTP(w, r)
			return
		}

		provided := extractAPIKey(r)
		if provided == "" || subtle.ConstantTimeCompare([]byte(provided), keyBytes) != 1 {
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusUnauthorized)
			json.NewEncoder(w).Encode(map[string]string{ //nolint:errcheck
				"error": "unauthorized: valid API key required",
			})
			return
		}

		next.ServeHTTP(w, r)
	})
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
