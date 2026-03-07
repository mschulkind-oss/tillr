package server

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestRateLimiter_Allow(t *testing.T) {
	rl := NewRateLimiter(10, 5) // 10 rps, burst 5
	defer rl.Stop()

	key := "192.168.1.1"

	// Burst of 5 should all succeed.
	for i := 0; i < 5; i++ {
		if !rl.Allow(key) {
			t.Fatalf("request %d should have been allowed within burst", i+1)
		}
	}

	// 6th request without waiting should be rejected.
	if rl.Allow(key) {
		t.Fatal("request 6 should have been rejected (burst exhausted)")
	}
}

func TestRateLimiter_DifferentKeys(t *testing.T) {
	rl := NewRateLimiter(10, 2) // burst 2
	defer rl.Stop()

	// Exhaust one IP.
	rl.Allow("ip-a")
	rl.Allow("ip-a")
	if rl.Allow("ip-a") {
		t.Fatal("ip-a should be exhausted")
	}

	// A different IP should still have its own bucket.
	if !rl.Allow("ip-b") {
		t.Fatal("ip-b should be allowed (independent bucket)")
	}
}

func TestRateLimiter_RetryAfter(t *testing.T) {
	rl := NewRateLimiter(1, 1) // 1 rps, burst 1
	defer rl.Stop()

	key := "10.0.0.1"
	rl.Allow(key) // consume the one token

	retry := rl.RetryAfter(key)
	if retry < 1 {
		t.Fatalf("expected Retry-After >= 1, got %d", retry)
	}
}

func TestRateLimitMiddleware_AllowsStaticFiles(t *testing.T) {
	rl := NewRateLimiter(1, 1) // very restrictive
	defer rl.Stop()

	inner := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	})
	handler := RateLimitMiddleware(rl, inner)

	// Static file requests should never be rate limited.
	for i := 0; i < 10; i++ {
		req := httptest.NewRequest("GET", "/style.css", nil)
		req.RemoteAddr = "1.2.3.4:9999"
		rr := httptest.NewRecorder()
		handler.ServeHTTP(rr, req)
		if rr.Code != http.StatusOK {
			t.Fatalf("static request %d got status %d, want 200", i+1, rr.Code)
		}
	}
}

func TestRateLimitMiddleware_AllowsWebSocket(t *testing.T) {
	rl := NewRateLimiter(1, 1)
	defer rl.Stop()

	inner := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	})
	handler := RateLimitMiddleware(rl, inner)

	for i := 0; i < 5; i++ {
		req := httptest.NewRequest("GET", "/ws", nil)
		req.RemoteAddr = "1.2.3.4:9999"
		rr := httptest.NewRecorder()
		handler.ServeHTTP(rr, req)
		if rr.Code != http.StatusOK {
			t.Fatalf("/ws request %d got status %d, want 200", i+1, rr.Code)
		}
	}
}

func TestRateLimitMiddleware_Returns429(t *testing.T) {
	rl := NewRateLimiter(1, 1) // 1 rps, burst 1
	defer rl.Stop()

	inner := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	})
	handler := RateLimitMiddleware(rl, inner)

	// First API request allowed.
	req := httptest.NewRequest("GET", "/api/status", nil)
	req.RemoteAddr = "5.6.7.8:1234"
	rr := httptest.NewRecorder()
	handler.ServeHTTP(rr, req)
	if rr.Code != http.StatusOK {
		t.Fatalf("first request: got %d, want 200", rr.Code)
	}

	// Second should be rejected.
	req = httptest.NewRequest("GET", "/api/features", nil)
	req.RemoteAddr = "5.6.7.8:1234"
	rr = httptest.NewRecorder()
	handler.ServeHTTP(rr, req)
	if rr.Code != http.StatusTooManyRequests {
		t.Fatalf("second request: got %d, want 429", rr.Code)
	}

	// Verify JSON body.
	var body map[string]string
	if err := json.NewDecoder(rr.Body).Decode(&body); err != nil {
		t.Fatalf("decoding 429 body: %v", err)
	}
	if body["error"] != "rate limit exceeded" {
		t.Fatalf("unexpected error message: %q", body["error"])
	}

	// Verify Retry-After header.
	ra := rr.Header().Get("Retry-After")
	if ra == "" {
		t.Fatal("missing Retry-After header on 429 response")
	}
}

func TestClientIP(t *testing.T) {
	tests := []struct {
		name       string
		remoteAddr string
		xff        string
		xri        string
		want       string
	}{
		{"plain remote addr", "192.168.1.1:12345", "", "", "192.168.1.1"},
		{"X-Forwarded-For single", "10.0.0.1:1", "203.0.113.50", "", "203.0.113.50"},
		{"X-Forwarded-For chain", "10.0.0.1:1", "203.0.113.50, 70.41.3.18", "", "203.0.113.50"},
		{"X-Real-IP", "10.0.0.1:1", "", "198.51.100.10", "198.51.100.10"},
		{"XFF takes precedence over XRI", "10.0.0.1:1", "1.1.1.1", "2.2.2.2", "1.1.1.1"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			req := httptest.NewRequest("GET", "/", nil)
			req.RemoteAddr = tt.remoteAddr
			if tt.xff != "" {
				req.Header.Set("X-Forwarded-For", tt.xff)
			}
			if tt.xri != "" {
				req.Header.Set("X-Real-IP", tt.xri)
			}
			got := clientIP(req)
			if got != tt.want {
				t.Errorf("clientIP() = %q, want %q", got, tt.want)
			}
		})
	}
}
