package claude

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"math"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

type roundTripFunc func(*http.Request) (*http.Response, error)

func (f roundTripFunc) RoundTrip(r *http.Request) (*http.Response, error) {
	return f(r)
}

func strPtr(s string) *string { return &s }

func TestDirectRequestNormalizesBothWindowsWithoutModelCall(t *testing.T) {
	t.Setenv("CLAUDE_CODE_OAUTH_TOKEN", "fixture-secret")
	t.Setenv("CLAUDE_SECURESTORAGE_CONFIG_DIR", "")
	t.Setenv("CLAUDE_CONFIG_DIR", "")

	payload := map[string]any{
		"five_hour": map[string]any{"utilization": 80, "resets_at": "2026-11-18T01:00:00Z"},
		"seven_day": map[string]any{"utilization": 33.5, "resets_at": "2026-11-24T01:00:00Z"},
	}
	raw, err := json.Marshal(payload)
	if err != nil {
		t.Fatal(err)
	}
	var gotReq *http.Request
	client := &http.Client{
		CheckRedirect: noRedirect,
		Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
			gotReq = r
			return &http.Response{
				StatusCode: http.StatusOK,
				Body:       io.NopCloser(bytes.NewReader(raw)),
				Header:     make(http.Header),
			}, nil
		}),
	}
	result, err := ReadQuota(context.Background(), client, "")
	if err != nil {
		t.Fatalf("ReadQuota: %v", err)
	}
	if gotReq == nil {
		t.Fatal("expected HTTP request")
	}
	if gotReq.Method != http.MethodGet {
		t.Errorf("method = %q, want GET", gotReq.Method)
	}
	if gotReq.URL.String() != "https://api.anthropic.com/api/oauth/usage" {
		t.Errorf("url = %q", gotReq.URL.String())
	}
	if got := gotReq.Header.Get("Authorization"); got != "Bearer fixture-secret" {
		t.Errorf("Authorization = %q, want Bearer fixture-secret", got)
	}
	if got := gotReq.Header.Get("Anthropic-beta"); got != "oauth-2025-04-20" {
		t.Errorf("Anthropic-beta = %q, want oauth-2025-04-20", got)
	}
	want := []QuotaWindow{
		{Kind: "five_hour", Fraction: 0.8, ResetAt: strPtr("2026-11-18T01:00:00Z")},
		{Kind: "seven_day", Fraction: 0.335, ResetAt: strPtr("2026-11-24T01:00:00Z")},
	}
	if !reflect.DeepEqual(result.Windows, want) {
		t.Fatalf("windows = %+v, want %+v", result.Windows, want)
	}
}

func TestReadsExistingSecureStorageWithoutRefreshingIt(t *testing.T) {
	root := t.TempDir()
	path := filepath.Join(root, ".credentials.json")
	contents := `{"claudeAiOauth":{"accessToken":"fixture-token","refreshToken":"never-spend"}}`
	if err := os.WriteFile(path, []byte(contents), 0600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("CLAUDE_CODE_OAUTH_TOKEN", "")
	t.Setenv("CLAUDE_SECURESTORAGE_CONFIG_DIR", root)
	t.Setenv("CLAUDE_CONFIG_DIR", "")

	tok, err := AccessToken()
	if err != nil {
		t.Fatalf("AccessToken: %v", err)
	}
	if tok != "fixture-token" {
		t.Fatalf("token = %q, want fixture-token", tok)
	}
	after, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if string(after) != contents {
		t.Fatalf("credentials file modified: got %q, want %q", string(after), contents)
	}
}

func TestMissingAuthIsAnErrorNotAModelProbe(t *testing.T) {
	root := t.TempDir()
	t.Setenv("CLAUDE_CODE_OAUTH_TOKEN", "")
	t.Setenv("CLAUDE_SECURESTORAGE_CONFIG_DIR", "")
	t.Setenv("CLAUDE_CONFIG_DIR", "")
	t.Setenv("HOME", root)

	_, err := AccessToken()
	if err == nil || !strings.Contains(err.Error(), "subscription access token") {
		t.Fatalf("expected subscription access token error, got %v", err)
	}
}

func TestBadAndUnknownWindowsAreRefused(t *testing.T) {
	payloads := []any{
		map[string]any{},
		map[string]any{"five_hour": nil, "seven_day": nil},
		map[string]any{"five_hour": map[string]any{"utilization": 0, "resets_at": nil}},
		map[string]any{"five_hour": map[string]any{"utilization": -1, "resets_at": "2026-11-18T01:00:00Z"}},
		map[string]any{"five_hour": map[string]any{"utilization": true, "resets_at": "2026-11-18T01:00:00Z"}},
		map[string]any{"five_hour": map[string]any{"utilization": 80, "resets_at": "bad"}},
		map[string]any{"five_hour": map[string]any{"utilization": 80}},
		map[string]any{"five_hour": map[string]any{"utilization": math.NaN(), "resets_at": "2026-11-18T01:00:00Z"}},
	}
	for i, p := range payloads {
		if _, err := NormalizeQuota(p); err == nil {
			t.Errorf("case %d: expected error for %#v", i, p)
		}
	}
	// Exercise window validation separately from missing-key validation.
	cases := []struct {
		used  any
		reset any
	}{
		{true, "2026-11-18T01:00:00Z"},
		{-1, nil},
		{math.NaN(), "2026-11-18T01:00:00Z"},
		{80, "bad"},
		{80, nil},
	}
	for i, tc := range cases {
		p := map[string]any{
			"five_hour": map[string]any{"utilization": tc.used, "resets_at": tc.reset},
			"seven_day": nil,
		}
		if _, err := NormalizeQuota(p); err == nil {
			t.Errorf("window case %d (%v, %v): expected error", i, tc.used, tc.reset)
		}
	}
}

func TestNullWindowIsOmittedNotInvented(t *testing.T) {
	result, err := NormalizeQuota(map[string]any{
		"five_hour": nil,
		"seven_day": map[string]any{"utilization": 10, "resets_at": "2026-11-24T01:00:00Z"},
	})
	if err != nil {
		t.Fatalf("NormalizeQuota: %v", err)
	}
	if len(result.Windows) != 1 || result.Windows[0].Kind != "seven_day" {
		t.Fatalf("windows = %+v", result.Windows)
	}
}

func TestIdleWindowHasNoInventedReset(t *testing.T) {
	result, err := NormalizeQuota(map[string]any{
		"five_hour": map[string]any{"utilization": 0, "resets_at": nil},
		"seven_day": nil,
	})
	if err != nil {
		t.Fatalf("NormalizeQuota: %v", err)
	}
	want := []QuotaWindow{{Kind: "five_hour", Fraction: 0, ResetAt: nil}}
	if !reflect.DeepEqual(result.Windows, want) {
		t.Fatalf("windows = %+v, want %+v", result.Windows, want)
	}
}

func TestDoesNotRedirectTheBearerToken(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, "https://other.invalid", http.StatusFound)
	}))
	defer srv.Close()

	t.Setenv("CLAUDE_CODE_OAUTH_TOKEN", "fixture-secret")
	_, err := ReadQuota(context.Background(), nil, srv.URL)
	if err == nil || !strings.Contains(err.Error(), "redirect") {
		t.Fatalf("expected redirect refusal, got %v", err)
	}
}

func TestRunQuotaRefusesArgumentsWithExit2(t *testing.T) {
	var stdout, stderr bytes.Buffer
	code := RunQuota([]string{"extra"}, &stdout, &stderr)
	if code != 2 {
		t.Fatalf("exit code = %d, want 2", code)
	}
	if !strings.Contains(stderr.String(), "usage: quota") {
		t.Fatalf("stderr = %q", stderr.String())
	}
}
