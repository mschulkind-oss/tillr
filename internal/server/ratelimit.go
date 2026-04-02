package server

import (
	"encoding/json"
	"math"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"time"
)

// tokenBucket implements a per-key token bucket rate limiter.
type tokenBucket struct {
	tokens     float64
	lastRefill time.Time
}

// RateLimiter tracks per-IP token buckets with periodic cleanup.
type RateLimiter struct {
	mu      sync.Mutex
	buckets map[string]*tokenBucket
	rate    float64 // tokens added per second
	burst   float64 // max tokens (bucket capacity)
	stopCh  chan struct{}
}

// NewRateLimiter creates a rate limiter with the given requests-per-second
// rate and burst capacity. It starts a background goroutine that removes
// stale entries every 5 minutes (IPs not seen in 10 minutes).
func NewRateLimiter(rps float64, burst int) *RateLimiter {
	rl := &RateLimiter{
		buckets: make(map[string]*tokenBucket),
		rate:    rps,
		burst:   float64(burst),
		stopCh:  make(chan struct{}),
	}
	go rl.cleanup()
	return rl
}

// Allow checks whether a request from the given key should be allowed.
// It returns true if a token was available, false otherwise.
func (rl *RateLimiter) Allow(key string) bool {
	rl.mu.Lock()
	defer rl.mu.Unlock()

	now := time.Now()
	b, ok := rl.buckets[key]
	if !ok {
		b = &tokenBucket{tokens: rl.burst, lastRefill: now}
		rl.buckets[key] = b
	}

	// Refill tokens based on elapsed time.
	elapsed := now.Sub(b.lastRefill).Seconds()
	b.tokens = math.Min(rl.burst, b.tokens+elapsed*rl.rate)
	b.lastRefill = now

	if b.tokens >= 1 {
		b.tokens--
		return true
	}
	return false
}

// Remaining returns the number of tokens currently available for key (without consuming).
func (rl *RateLimiter) Remaining(key string) int {
	rl.mu.Lock()
	defer rl.mu.Unlock()

	b, ok := rl.buckets[key]
	if !ok {
		return int(rl.burst)
	}

	elapsed := time.Since(b.lastRefill).Seconds()
	tokens := math.Min(rl.burst, b.tokens+elapsed*rl.rate)
	return int(math.Floor(tokens))
}

// ResetTime returns the Unix timestamp when the bucket will be fully refilled.
func (rl *RateLimiter) ResetTime(key string) int64 {
	rl.mu.Lock()
	defer rl.mu.Unlock()

	b, ok := rl.buckets[key]
	if !ok {
		return time.Now().Unix()
	}

	elapsed := time.Since(b.lastRefill).Seconds()
	tokens := math.Min(rl.burst, b.tokens+elapsed*rl.rate)
	if tokens >= rl.burst {
		return time.Now().Unix()
	}
	deficit := rl.burst - tokens
	secs := deficit / rl.rate
	return time.Now().Add(time.Duration(secs * float64(time.Second))).Unix()
}

// RetryAfter returns the number of seconds until a token is available for key.
func (rl *RateLimiter) RetryAfter(key string) int {
	rl.mu.Lock()
	defer rl.mu.Unlock()

	b, ok := rl.buckets[key]
	if !ok {
		return 0
	}

	elapsed := time.Since(b.lastRefill).Seconds()
	tokens := math.Min(rl.burst, b.tokens+elapsed*rl.rate)
	if tokens >= 1 {
		return 0
	}
	deficit := 1.0 - tokens
	secs := deficit / rl.rate
	return int(math.Ceil(secs))
}

// Limit returns the burst capacity (max tokens).
func (rl *RateLimiter) Limit() int {
	return int(rl.burst)
}

// Stop terminates the background cleanup goroutine.
func (rl *RateLimiter) Stop() {
	close(rl.stopCh)
}

// cleanup removes entries not seen in 10 minutes, running every 5 minutes.
func (rl *RateLimiter) cleanup() {
	ticker := time.NewTicker(5 * time.Minute)
	defer ticker.Stop()
	for {
		select {
		case <-rl.stopCh:
			return
		case <-ticker.C:
			rl.mu.Lock()
			cutoff := time.Now().Add(-10 * time.Minute)
			for key, b := range rl.buckets {
				if b.lastRefill.Before(cutoff) {
					delete(rl.buckets, key)
				}
			}
			rl.mu.Unlock()
		}
	}
}

// rateLimitKey determines the rate limiting key for a request.
// Uses API key if present (from Bearer token or query param), otherwise client IP.
func rateLimitKey(r *http.Request) string {
	// Check for API key first (per-key limiting)
	if auth := r.Header.Get("Authorization"); auth != "" {
		if after, ok := strings.CutPrefix(auth, "Bearer "); ok {
			token := strings.TrimSpace(after)
			if token != "" {
				return "apikey:" + token
			}
		}
	}
	if qk := r.URL.Query().Get("api_key"); qk != "" {
		return "apikey:" + qk
	}
	// Fall back to client IP
	return "ip:" + clientIP(r)
}

// clientIP extracts the client IP from the request, respecting X-Forwarded-For
// and X-Real-IP headers, and stripping the port from RemoteAddr.
func clientIP(r *http.Request) string {
	if xff := r.Header.Get("X-Forwarded-For"); xff != "" {
		// First entry is the original client.
		if ip, _, ok := strings.Cut(xff, ","); ok {
			return strings.TrimSpace(ip)
		}
		return strings.TrimSpace(xff)
	}
	if xri := r.Header.Get("X-Real-IP"); xri != "" {
		return strings.TrimSpace(xri)
	}
	// Strip port from RemoteAddr (e.g. "192.168.1.1:12345" -> "192.168.1.1").
	ip := r.RemoteAddr
	if idx := strings.LastIndex(ip, ":"); idx != -1 {
		ip = ip[:idx]
	}
	return ip
}

// isExemptFromRateLimit returns true for paths that should not be rate limited.
func isExemptFromRateLimit(r *http.Request) bool {
	path := r.URL.Path
	// Exempt non-API routes (static files, SPA)
	if !strings.HasPrefix(path, "/api/") {
		return true
	}
	// Exempt health check
	if path == "/api/health" || path == "/api/docs" {
		return true
	}
	return false
}

// RateLimitMiddleware returns an http.Handler that applies rate limiting
// to /api/* paths only. Health check, docs, static files, and WebSocket
// connections are not limited. Adds standard rate limit headers to responses.
func RateLimitMiddleware(rl *RateLimiter, next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Skip WebSocket upgrades
		if r.URL.Path == "/ws" {
			next.ServeHTTP(w, r)
			return
		}

		if isExemptFromRateLimit(r) {
			next.ServeHTTP(w, r)
			return
		}

		key := rateLimitKey(r)

		// Always set rate limit headers on API responses
		w.Header().Set("X-RateLimit-Limit", strconv.Itoa(rl.Limit()))

		if !rl.Allow(key) {
			retry := rl.RetryAfter(key)
			w.Header().Set("X-RateLimit-Remaining", "0")
			w.Header().Set("X-RateLimit-Reset", strconv.FormatInt(rl.ResetTime(key), 10))
			w.Header().Set("Retry-After", strconv.Itoa(retry))
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusTooManyRequests)
			json.NewEncoder(w).Encode(map[string]string{ //nolint:errcheck
				"error": "rate limit exceeded",
			})
			return
		}

		// Set remaining/reset after allowing (token was consumed)
		w.Header().Set("X-RateLimit-Remaining", strconv.Itoa(rl.Remaining(key)))
		w.Header().Set("X-RateLimit-Reset", strconv.FormatInt(rl.ResetTime(key), 10))

		next.ServeHTTP(w, r)
	})
}
