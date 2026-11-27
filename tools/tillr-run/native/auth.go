package native

import (
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
)

const (
	defaultBedrockRegion = "us-east-1"
	maxAuthJSONBytes     = 1 << 20 // 1 MiB
)

// AuthRefusalError reports missing or invalid provider credentials that require
// native-agent to exit with ExitRefused (exit code 2).
type AuthRefusalError struct {
	Message string
}

func (e *AuthRefusalError) Error() string {
	return e.Message
}

// BedrockAuth holds resolved AWS Bedrock credentials in memory (either a
// bearer token or SigV4 IAM credentials).
type BedrockAuth struct {
	BearerToken     string
	AccessKeyID     string
	SecretAccessKey string
	SessionToken    string
	Source          string
}

// IsBearer reports whether authentication uses an AWS Bedrock bearer token.
func (a BedrockAuth) IsBearer() bool {
	return a.BearerToken != ""
}

// SigV4 returns SigV4Credentials for the given region.
func (a BedrockAuth) SigV4(region string) SigV4Credentials {
	return SigV4Credentials{
		AccessKeyID:     a.AccessKeyID,
		SecretAccessKey: a.SecretAccessKey,
		SessionToken:    a.SessionToken,
		Region:          region,
	}
}

// ParseBedrockModelID parses "amazon-bedrock/<modelId>" or "bedrock/<modelId>"
// and returns the underlying Bedrock model ID or inference-profile ARN.
func ParseBedrockModelID(model string) (modelID string, ok bool) {
	trimmed := strings.TrimSpace(model)
	for _, prefix := range []string{"amazon-bedrock/", "bedrock/"} {
		if rest, matched := strings.CutPrefix(trimmed, prefix); matched {
			if rest == "" {
				return "", false
			}
			return rest, true
		}
	}
	return "", false
}

// ResolveBedrockAuth resolves AWS Bedrock credentials from process environment
// variables (checking --account suffixed variables first) and, if unset,
// falls back to a strictly read-only lookup of pi's auth.json.
func ResolveBedrockAuth(model, account string, getenv func(string) string) (BedrockAuth, error) {
	if getenv == nil {
		getenv = func(string) string { return "" }
	}

	if slug := normalizeAccountSlug(account); slug != "" {
		if tok := strings.TrimSpace(getenv("AWS_BEARER_TOKEN_BEDROCK_" + slug)); tok != "" {
			return BedrockAuth{BearerToken: tok, Source: "env:account"}, nil
		}
		id := strings.TrimSpace(getenv("AWS_ACCESS_KEY_ID_" + slug))
		secret := strings.TrimSpace(getenv("AWS_SECRET_ACCESS_KEY_" + slug))
		if id != "" && secret != "" {
			return BedrockAuth{
				AccessKeyID:     id,
				SecretAccessKey: secret,
				SessionToken:    strings.TrimSpace(getenv("AWS_SESSION_TOKEN_" + slug)),
				Source:          "env:account",
			}, nil
		}
	}

	if tok := strings.TrimSpace(getenv("AWS_BEARER_TOKEN_BEDROCK")); tok != "" {
		return BedrockAuth{BearerToken: tok, Source: "env"}, nil
	}
	id := strings.TrimSpace(getenv("AWS_ACCESS_KEY_ID"))
	secret := strings.TrimSpace(getenv("AWS_SECRET_ACCESS_KEY"))
	if id != "" && secret != "" {
		return BedrockAuth{
			AccessKeyID:     id,
			SecretAccessKey: secret,
			SessionToken:    strings.TrimSpace(getenv("AWS_SESSION_TOKEN")),
			Source:          "env",
		}, nil
	}

	if tok, ok := readPiBedrockAPIKey(getenv); ok {
		return BedrockAuth{BearerToken: tok, Source: "pi-auth.json"}, nil
	}

	return BedrockAuth{}, &AuthRefusalError{
		Message: fmt.Sprintf(
			"missing bedrock credentials for model %s (expected AWS_ACCESS_KEY_ID + AWS_SECRET_ACCESS_KEY or AWS_BEARER_TOKEN_BEDROCK)",
			model,
		),
	}
}

// ResolveBedrockRegion resolves the AWS region for a Bedrock model ID or
// inference-profile ARN, falling back to account-suffixed AWS_REGION,
// AWS_REGION, AWS_DEFAULT_REGION, and finally "us-east-1".
func ResolveBedrockRegion(modelID, account string, getenv func(string) string) string {
	if parsed, ok := ParseBedrockModelID(modelID); ok {
		modelID = parsed
	}
	if region, ok := extractBedrockARNRegion(modelID); ok {
		return region
	}
	if getenv != nil {
		if slug := normalizeAccountSlug(account); slug != "" {
			if r := strings.TrimSpace(getenv("AWS_REGION_" + slug)); r != "" {
				return r
			}
		}
		if r := strings.TrimSpace(getenv("AWS_REGION")); r != "" {
			return r
		}
		if r := strings.TrimSpace(getenv("AWS_DEFAULT_REGION")); r != "" {
			return r
		}
	}
	return defaultBedrockRegion
}

func extractBedrockARNRegion(modelID string) (string, bool) {
	// ARN format: arn:aws*:bedrock:<region>:<account>:<resource>
	parts := strings.SplitN(strings.TrimSpace(modelID), ":", 6)
	if len(parts) < 5 {
		return "", false
	}
	if parts[0] != "arn" || !strings.HasPrefix(parts[1], "aws") || parts[2] != "bedrock" {
		return "", false
	}
	region := strings.TrimSpace(parts[3])
	if region == "" {
		return "", false
	}
	return region, true
}

func normalizeAccountSlug(account string) string {
	trimmed := strings.TrimSpace(account)
	if trimmed == "" {
		return ""
	}
	upper := strings.ToUpper(trimmed)
	var b strings.Builder
	b.Grow(len(upper))
	for i := 0; i < len(upper); i++ {
		c := upper[i]
		if (c >= 'A' && c <= 'Z') || (c >= '0' && c <= '9') {
			b.WriteByte(c)
		} else {
			b.WriteByte('_')
		}
	}
	return b.String()
}

func resolvePiAuthJSONPath(getenv func(string) string) (string, bool) {
	if dir := strings.TrimSpace(getenv("TILLR_DEPLOYED_PI_AGENT_DIR")); dir != "" {
		return filepath.Join(dir, "auth.json"), true
	}
	if dir := strings.TrimSpace(getenv("PI_CODING_AGENT_DIR")); dir != "" {
		return filepath.Join(dir, "auth.json"), true
	}
	if home := strings.TrimSpace(getenv("HOME")); home != "" {
		return filepath.Join(home, ".pi", "agent", "auth.json"), true
	}
	return "", false
}

func readPiBedrockAPIKey(getenv func(string) string) (string, bool) {
	authPath, ok := resolvePiAuthJSONPath(getenv)
	if !ok {
		return "", false
	}
	resolved, err := filepath.EvalSymlinks(authPath)
	if err != nil {
		return "", false
	}
	f, err := os.OpenFile(resolved, os.O_RDONLY, 0)
	if err != nil {
		return "", false
	}
	defer f.Close()

	raw, err := io.ReadAll(io.LimitReader(f, maxAuthJSONBytes))
	if err != nil {
		return "", false
	}

	var entries map[string]struct {
		Type   string `json:"type"`
		Key    string `json:"key"`
		APIKey string `json:"api_key"`
	}
	if err := json.Unmarshal(raw, &entries); err != nil {
		return "", false
	}
	entry, exists := entries["amazon-bedrock"]
	if !exists {
		return "", false
	}
	if k := strings.TrimSpace(entry.APIKey); k != "" {
		return k, true
	}
	if k := strings.TrimSpace(entry.Key); k != "" {
		return k, true
	}
	return "", false
}
