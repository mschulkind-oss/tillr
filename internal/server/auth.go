package server

import (
	"crypto/subtle"
	"encoding/json"
	"net/http"
	"strings"
)

// AuthMiddleware requires a valid API key for /api/* routes, except
// /api/health which remains public. WebSocket and static files are not
// affected. Empty apiKey disables auth entirely (handled by caller).
func AuthMiddleware(apiKey string, next http.Handler) http.Handler {
	keyBytes := []byte(apiKey)
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !strings.HasPrefix(r.URL.Path, "/api/") || r.URL.Path == "/api/health" {
			next.ServeHTTP(w, r)
			return
		}
		if r.Method == http.MethodOptions {
			next.ServeHTTP(w, r)
			return
		}
		provided := extractAPIKey(r)
		if provided != "" && subtle.ConstantTimeCompare([]byte(provided), keyBytes) == 1 {
			next.ServeHTTP(w, r)
			return
		}
		unauthorizedResponse(w)
	})
}

func unauthorizedResponse(w http.ResponseWriter) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusUnauthorized)
	_ = json.NewEncoder(w).Encode(map[string]string{
		"error": "unauthorized: valid API key required",
	})
}

// extractAPIKey reads the API key from `Authorization: Bearer ...` or
// the `api_key` query param.
func extractAPIKey(r *http.Request) string {
	if auth := r.Header.Get("Authorization"); auth != "" {
		if after, ok := strings.CutPrefix(auth, "Bearer "); ok {
			return strings.TrimSpace(after)
		}
	}
	return r.URL.Query().Get("api_key")
}
