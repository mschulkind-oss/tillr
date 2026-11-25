// Package claude implements the Claude Code adapter's Go helpers: subscription quota
// reading (`tillr-run claude-quota`) and the suspended delivery call server and hooks
// (`tillr-run claude-landing`).
package claude

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"
)

const (
	quotaURL       = "https://api.anthropic.com/api/oauth/usage"
	quotaBodyLimit = 64 * 1024
)

// QuotaWindow is one normalized subscription usage window.
type QuotaWindow struct {
	Kind     string  `json:"kind"`
	Fraction float64 `json:"fraction"`
	ResetAt  *string `json:"reset_at"`
}

// QuotaResult is the JSON document printed by `tillr-run claude-quota`.
type QuotaResult struct {
	Windows []QuotaWindow `json:"windows"`
}

type httpStatusError struct {
	code int
}

func (e *httpStatusError) Error() string {
	return fmt.Sprintf("HTTP %d; no reading recorded as open", e.code)
}

func noRedirect(_ *http.Request, _ []*http.Request) error {
	return errors.New("quota endpoint redirect refused")
}

// AccessToken reads the already-issued Claude subscription OAuth token from the
// environment or Claude Code's credentials file. It never refreshes credentials
// or starts a model session.
func AccessToken() (string, error) {
	if token := os.Getenv("CLAUDE_CODE_OAUTH_TOKEN"); token != "" {
		return token, nil
	}
	root := os.Getenv("CLAUDE_SECURESTORAGE_CONFIG_DIR")
	if root == "" {
		root = os.Getenv("CLAUDE_CONFIG_DIR")
	}
	var credPath string
	if root != "" {
		credPath = filepath.Join(root, ".credentials.json")
	} else {
		home := os.Getenv("HOME")
		if home == "" {
			if h, err := os.UserHomeDir(); err == nil {
				home = h
			}
		}
		credPath = filepath.Join(home, ".claude", ".credentials.json")
	}
	var token string
	if raw, err := os.ReadFile(credPath); err == nil {
		var creds map[string]any
		if json.Unmarshal(raw, &creds) == nil {
			if oauth, ok := creds["claudeAiOauth"].(map[string]any); ok {
				if tok, ok := oauth["accessToken"].(string); ok {
					token = tok
				}
			}
		}
	}
	if token == "" {
		return "", errors.New("no subscription access token: sign in to Claude Code; quota never starts a model session")
	}
	return token, nil
}

func parseQuotaReset(raw string) (string, error) {
	// Accept RFC3339 / RFC3339Nano (including Z or numeric offset).
	if t, err := time.Parse(time.RFC3339Nano, raw); err == nil {
		utc := t.UTC()
		if utc.Nanosecond() == 0 {
			return utc.Format(time.RFC3339), nil
		}
		return utc.Format(time.RFC3339Nano), nil
	}
	// Accept space separator as Python's datetime.fromisoformat does.
	if strings.Contains(raw, " ") {
		if t, err := time.Parse(time.RFC3339Nano, strings.Replace(raw, " ", "T", 1)); err == nil {
			utc := t.UTC()
			if utc.Nanosecond() == 0 {
				return utc.Format(time.RFC3339), nil
			}
			return utc.Format(time.RFC3339Nano), nil
		}
	}
	// Detect valid naive ISO timestamps (missing time zone) for the specific error message.
	for _, layout := range []string{"2006-01-02T15:04:05.999999999", "2006-01-02 15:04:05.999999999", "2006-01-02"} {
		if _, err := time.Parse(layout, raw); err == nil {
			return "", errors.New("quota reset time needs a time zone")
		}
	}
	return "", errors.New("invalid quota reset time")
}

// NormalizeQuota validates and normalizes the raw quota response object.
func NormalizeQuota(payload any) (QuotaResult, error) {
	obj, ok := payload.(map[string]any)
	if !ok {
		return QuotaResult{}, errors.New("quota response must name both known windows (null only when absent)")
	}
	for _, kind := range []string{"five_hour", "seven_day"} {
		if _, exists := obj[kind]; !exists {
			return QuotaResult{}, errors.New("quota response must name both known windows (null only when absent)")
		}
	}
	var windows []QuotaWindow
	for _, kind := range []string{"five_hour", "seven_day"} {
		rawWindow := obj[kind]
		if rawWindow == nil {
			continue
		}
		window, ok := rawWindow.(map[string]any)
		if !ok {
			return QuotaResult{}, errors.New("invalid quota window")
		}
		usedVal, exists := window["utilization"]
		if !exists {
			return QuotaResult{}, errors.New("invalid quota utilization")
		}
		var used float64
		switch v := usedVal.(type) {
		case float64:
			used = v
		case int:
			used = float64(v)
		case int64:
			used = float64(v)
		default:
			return QuotaResult{}, errors.New("invalid quota utilization")
		}
		if math.IsNaN(used) || math.IsInf(used, 0) || used < 0 {
			return QuotaResult{}, errors.New("invalid quota utilization")
		}
		resetVal := window["resets_at"]
		if resetVal == nil && used == 0 {
			windows = append(windows, QuotaWindow{Kind: kind, Fraction: 0, ResetAt: nil})
			continue
		}
		resetStr, ok := resetVal.(string)
		if !ok {
			return QuotaResult{}, errors.New("quota window has no reset time")
		}
		resetUTC, err := parseQuotaReset(resetStr)
		if err != nil {
			return QuotaResult{}, err
		}
		windows = append(windows, QuotaWindow{Kind: kind, Fraction: used / 100.0, ResetAt: &resetUTC})
	}
	if len(windows) == 0 {
		return QuotaResult{}, errors.New("quota response has no known windows")
	}
	return QuotaResult{Windows: windows}, nil
}

// ReadQuota fetches and normalizes the subscription usage windows with one
// authenticated GET. Never follows redirects or prints response bodies.
func ReadQuota(ctx context.Context, client *http.Client, endpoint string) (QuotaResult, error) {
	token, err := AccessToken()
	if err != nil {
		return QuotaResult{}, err
	}
	if endpoint == "" {
		endpoint = quotaURL
	}
	if client == nil {
		client = &http.Client{
			Timeout:       10 * time.Second,
			CheckRedirect: noRedirect,
		}
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
	if err != nil {
		return QuotaResult{}, err
	}
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("anthropic-beta", "oauth-2025-04-20")
	req.Header.Set("User-Agent", "claude-cli/2.1.319 (external, cli)")
	req.Header.Set("Content-Type", "application/json")

	resp, err := client.Do(req)
	if err != nil {
		return QuotaResult{}, err
	}
	defer resp.Body.Close()

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return QuotaResult{}, &httpStatusError{code: resp.StatusCode}
	}
	raw, err := io.ReadAll(io.LimitReader(resp.Body, quotaBodyLimit+1))
	if err != nil {
		return QuotaResult{}, err
	}
	if len(raw) > quotaBodyLimit {
		return QuotaResult{}, errors.New("quota response exceeds 64 KiB")
	}
	var payload any
	if err := json.Unmarshal(raw, &payload); err != nil {
		return QuotaResult{}, err
	}
	return NormalizeQuota(payload)
}

// RunQuota is the CLI entry point for `tillr-run claude-quota`.
func RunQuota(args []string, stdout, stderr io.Writer) int {
	if len(args) != 0 {
		fmt.Fprintln(stderr, "usage: quota (reads Claude subscription usage; no model tokens)")
		return 2
	}
	result, err := ReadQuota(context.Background(), nil, "")
	if err != nil {
		var statusErr *httpStatusError
		if errors.As(err, &statusErr) {
			fmt.Fprintf(stderr, "claude quota: HTTP %d; no reading recorded as open\n", statusErr.code)
			return 1
		}
		fmt.Fprintf(stderr, "claude quota: %v\n", err)
		return 1
	}
	encoded, err := json.Marshal(result)
	if err != nil {
		fmt.Fprintf(stderr, "claude quota: %v\n", err)
		return 1
	}
	fmt.Fprintln(stdout, string(encoded))
	return 0
}
