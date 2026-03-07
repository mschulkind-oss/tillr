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
	// Strip port from RemoteAddr (e.g. "192.168.1.1:12345" → "192.168.1.1").
	ip := r.RemoteAddr
	if idx := strings.LastIndex(ip, ":"); idx != -1 {
		ip = ip[:idx]
	}
	return ip
}

// RateLimitMiddleware returns an http.Handler that applies rate limiting
// to /api/* paths only. Static files and WebSocket connections are not limited.
func RateLimitMiddleware(rl *RateLimiter, next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.HasPrefix(r.URL.Path, "/api/") {
			ip := clientIP(r)
			if !rl.Allow(ip) {
				retry := rl.RetryAfter(ip)
				w.Header().Set("Content-Type", "application/json")
				w.Header().Set("Retry-After", strconv.Itoa(retry))
				w.WriteHeader(http.StatusTooManyRequests)
				json.NewEncoder(w).Encode(map[string]string{ //nolint:errcheck
					"error": "rate limit exceeded",
				})
				return
			}
		}
		next.ServeHTTP(w, r)
	})
}
