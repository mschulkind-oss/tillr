package native

import (
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func mapGetenv(env map[string]string) func(string) string {
	return func(key string) string {
		return env[key]
	}
}

func TestParseBedrockModelID(t *testing.T) {
	cases := []struct {
		name   string
		input  string
		wantID string
		wantOK bool
	}{
		{
			name:   "amazon-bedrock standard model",
			input:  "amazon-bedrock/anthropic.claude-3-5-sonnet-20241022-v2:0",
			wantID: "anthropic.claude-3-5-sonnet-20241022-v2:0",
			wantOK: true,
		},
		{
			name:   "bedrock short prefix",
			input:  "bedrock/us.anthropic.claude-sonnet-4-20250514-v1:0",
			wantID: "us.anthropic.claude-sonnet-4-20250514-v1:0",
			wantOK: true,
		},
		{
			name:   "amazon-bedrock inference profile ARN",
			input:  "amazon-bedrock/arn:aws:bedrock:us-west-2:123456789012:inference-profile/us.anthropic.claude-sonnet-4-20250514-v1:0",
			wantID: "arn:aws:bedrock:us-west-2:123456789012:inference-profile/us.anthropic.claude-sonnet-4-20250514-v1:0",
			wantOK: true,
		},
		{
			name:   "empty model id after amazon-bedrock prefix",
			input:  "amazon-bedrock/",
			wantID: "",
			wantOK: false,
		},
		{
			name:   "empty model id after bedrock prefix",
			input:  "bedrock/",
			wantID: "",
			wantOK: false,
		},
		{
			name:   "other provider prefix",
			input:  "github-copilot/gpt-5",
			wantID: "",
			wantOK: false,
		},
		{
			name:   "unprefixed model",
			input:  "claude-sonnet-4",
			wantID: "",
			wantOK: false,
		},
		{
			name:   "empty string",
			input:  "",
			wantID: "",
			wantOK: false,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			gotID, gotOK := ParseBedrockModelID(tc.input)
			if gotID != tc.wantID || gotOK != tc.wantOK {
				t.Fatalf("ParseBedrockModelID(%q) = (%q, %v), want (%q, %v)",
					tc.input, gotID, gotOK, tc.wantID, tc.wantOK)
			}
		})
	}
}

func TestBedrockAuth(t *testing.T) {
	const model = "amazon-bedrock/us.anthropic.claude-sonnet-4-20250514-v1:0"

	t.Run("account-suffixed bearer token takes precedence", func(t *testing.T) {
		env := map[string]string{
			"AWS_BEARER_TOKEN_BEDROCK_PROD_EAST_1": "acct-bearer",
			"AWS_ACCESS_KEY_ID_PROD_EAST_1":        "AKIAACCT",
			"AWS_SECRET_ACCESS_KEY_PROD_EAST_1":    "secret-acct",
			"AWS_BEARER_TOKEN_BEDROCK":             "std-bearer",
			"AWS_ACCESS_KEY_ID":                    "AKIASTD",
			"AWS_SECRET_ACCESS_KEY":                "secret-std",
		}
		got, err := ResolveBedrockAuth(model, "prod-east.1", mapGetenv(env))
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if !got.IsBearer() || got.BearerToken != "acct-bearer" {
			t.Fatalf("got %+v, want bearer token acct-bearer", got)
		}
	})

	t.Run("account-suffixed SigV4 with session token", func(t *testing.T) {
		env := map[string]string{
			"AWS_ACCESS_KEY_ID_WORK_2":     "AKIAWORK",
			"AWS_SECRET_ACCESS_KEY_WORK_2": "secret-work",
			"AWS_SESSION_TOKEN_WORK_2":     "sess-work",
			"AWS_ACCESS_KEY_ID":            "AKIASTD",
			"AWS_SECRET_ACCESS_KEY":        "secret-std",
		}
		got, err := ResolveBedrockAuth(model, "work/2", mapGetenv(env))
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if got.IsBearer() {
			t.Fatalf("expected SigV4 credentials, got bearer")
		}
		sig := got.SigV4("us-east-1")
		if sig.AccessKeyID != "AKIAWORK" || sig.SecretAccessKey != "secret-work" || sig.SessionToken != "sess-work" {
			t.Fatalf("unexpected SigV4 credentials: %+v", sig)
		}
	})

	t.Run("incomplete account-suffixed SigV4 falls back to standard env", func(t *testing.T) {
		env := map[string]string{
			"AWS_ACCESS_KEY_ID_DEV": "AKIADEV",
			"AWS_ACCESS_KEY_ID":     "AKIASTD",
			"AWS_SECRET_ACCESS_KEY": "secret-std",
		}
		got, err := ResolveBedrockAuth(model, "dev", mapGetenv(env))
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if got.AccessKeyID != "AKIASTD" || got.SecretAccessKey != "secret-std" {
			t.Fatalf("got %+v, want standard SigV4", got)
		}
	})

	t.Run("standard bearer token over standard SigV4", func(t *testing.T) {
		env := map[string]string{
			"AWS_BEARER_TOKEN_BEDROCK": "std-bearer",
			"AWS_ACCESS_KEY_ID":        "AKIASTD",
			"AWS_SECRET_ACCESS_KEY":    "secret-std",
		}
		got, err := ResolveBedrockAuth(model, "", mapGetenv(env))
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if !got.IsBearer() || got.BearerToken != "std-bearer" {
			t.Fatalf("got %+v, want std-bearer", got)
		}
	})

	t.Run("standard SigV4 without session token", func(t *testing.T) {
		env := map[string]string{
			"AWS_ACCESS_KEY_ID":     "AKIASTD",
			"AWS_SECRET_ACCESS_KEY": "secret-std",
		}
		got, err := ResolveBedrockAuth(model, "", mapGetenv(env))
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if got.AccessKeyID != "AKIASTD" || got.SecretAccessKey != "secret-std" || got.SessionToken != "" {
			t.Fatalf("unexpected SigV4: %+v", got)
		}
	})

	t.Run("secondary read-only pi auth.json lookup via symlink", func(t *testing.T) {
		tmp := t.TempDir()
		sharedDir := filepath.Join(tmp, "shared")
		if err := os.MkdirAll(sharedDir, 0o755); err != nil {
			t.Fatal(err)
		}
		realAuth := filepath.Join(sharedDir, "auth.json")
		payload := []byte(`{"amazon-bedrock":{"type":"api_key","api_key":"pi-bedrock-key"}}`)
		if err := os.WriteFile(realAuth, payload, 0o600); err != nil {
			t.Fatal(err)
		}
		past := time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC)
		if err := os.Chtimes(realAuth, past, past); err != nil {
			t.Fatal(err)
		}

		agentDir := filepath.Join(tmp, "deployed-pi")
		if err := os.MkdirAll(agentDir, 0o755); err != nil {
			t.Fatal(err)
		}
		linkAuth := filepath.Join(agentDir, "auth.json")
		if err := os.Symlink(realAuth, linkAuth); err != nil {
			t.Fatal(err)
		}

		env := map[string]string{
			"TILLR_DEPLOYED_PI_AGENT_DIR": agentDir,
		}
		got, err := ResolveBedrockAuth(model, "", mapGetenv(env))
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if !got.IsBearer() || got.BearerToken != "pi-bedrock-key" {
			t.Fatalf("got %+v, want pi-bedrock-key", got)
		}

		info, err := os.Stat(realAuth)
		if err != nil {
			t.Fatal(err)
		}
		if !info.ModTime().Equal(past) {
			t.Fatalf("auth.json mtime modified: got %v, want %v", info.ModTime(), past)
		}
		afterBytes, err := os.ReadFile(realAuth)
		if err != nil {
			t.Fatal(err)
		}
		if string(afterBytes) != string(payload) {
			t.Fatalf("auth.json contents modified")
		}
	})

	t.Run("pi auth.json precedence and key field", func(t *testing.T) {
		tmp := t.TempDir()
		deployedDir := filepath.Join(tmp, "deployed")
		codingDir := filepath.Join(tmp, "coding")
		homeDir := filepath.Join(tmp, "home")
		for _, d := range []string{deployedDir, codingDir, filepath.Join(homeDir, ".pi", "agent")} {
			if err := os.MkdirAll(d, 0o755); err != nil {
				t.Fatal(err)
			}
		}
		if err := os.WriteFile(filepath.Join(codingDir, "auth.json"), []byte(`{"amazon-bedrock":{"key":"from-coding"}}`), 0o600); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(homeDir, ".pi", "agent", "auth.json"), []byte(`{"amazon-bedrock":{"key":"from-home"}}`), 0o600); err != nil {
			t.Fatal(err)
		}

		// PI_CODING_AGENT_DIR wins over HOME when TILLR_DEPLOYED_PI_AGENT_DIR is unset.
		got, err := ResolveBedrockAuth(model, "", mapGetenv(map[string]string{
			"PI_CODING_AGENT_DIR": codingDir,
			"HOME":                homeDir,
		}))
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if got.BearerToken != "from-coding" {
			t.Fatalf("got %q, want from-coding", got.BearerToken)
		}

		// HOME/.pi/agent/auth.json is used when neither pi dir env var is set.
		got, err = ResolveBedrockAuth(model, "", mapGetenv(map[string]string{
			"HOME": homeDir,
		}))
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if got.BearerToken != "from-home" {
			t.Fatalf("got %q, want from-home", got.BearerToken)
		}

		// TILLR_DEPLOYED_PI_AGENT_DIR wins over PI_CODING_AGENT_DIR and HOME.
		if err := os.WriteFile(filepath.Join(deployedDir, "auth.json"), []byte(`{"amazon-bedrock":{"api_key":"from-deployed"}}`), 0o600); err != nil {
			t.Fatal(err)
		}
		got, err = ResolveBedrockAuth(model, "", mapGetenv(map[string]string{
			"TILLR_DEPLOYED_PI_AGENT_DIR": deployedDir,
			"PI_CODING_AGENT_DIR":         codingDir,
			"HOME":                        homeDir,
		}))
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if got.BearerToken != "from-deployed" {
			t.Fatalf("got %q, want from-deployed", got.BearerToken)
		}
	})

	t.Run("missing credentials returns typed AuthRefusalError with exact message", func(t *testing.T) {
		tmp := t.TempDir()
		if err := os.WriteFile(filepath.Join(tmp, "auth.json"), []byte(`{"github-copilot":{"access":"a","refresh":"r"}}`), 0o600); err != nil {
			t.Fatal(err)
		}

		_, err := ResolveBedrockAuth(model, "acct", mapGetenv(map[string]string{
			"PI_CODING_AGENT_DIR": tmp,
		}))
		if err == nil {
			t.Fatalf("expected error, got nil")
		}
		var refusal *AuthRefusalError
		if !errors.As(err, &refusal) {
			t.Fatalf("expected *AuthRefusalError, got %T: %v", err, err)
		}
		wantMsg := "missing bedrock credentials for model " + model + " (expected AWS_ACCESS_KEY_ID + AWS_SECRET_ACCESS_KEY or AWS_BEARER_TOKEN_BEDROCK)"
		if refusal.Error() != wantMsg {
			t.Fatalf("got message %q, want %q", refusal.Error(), wantMsg)
		}
	})
}

func TestBedrockRegion(t *testing.T) {
	cases := []struct {
		name    string
		modelID string
		account string
		env     map[string]string
		want    string
	}{
		{
			name:    "inference-profile ARN overrides env",
			modelID: "arn:aws:bedrock:us-west-2:123456789012:inference-profile/us.anthropic.claude-sonnet-4-20250514-v1:0",
			account: "prod",
			env: map[string]string{
				"AWS_REGION_PROD":    "eu-central-1",
				"AWS_REGION":         "ap-southeast-1",
				"AWS_DEFAULT_REGION": "us-east-2",
			},
			want: "us-west-2",
		},
		{
			name:    "govcloud ARN",
			modelID: "arn:aws-us-gov:bedrock:us-gov-west-1:123456789012:application-inference-profile/abc",
			want:    "us-gov-west-1",
		},
		{
			name:    "prefixed ARN",
			modelID: "amazon-bedrock/arn:aws:bedrock:eu-west-1:123456789012:inference-profile/eu.anthropic.claude-sonnet-4-v1:0",
			want:    "eu-west-1",
		},
		{
			name:    "account-suffixed AWS_REGION",
			modelID: "anthropic.claude-3-5-sonnet-20241022-v2:0",
			account: "prod-west",
			env: map[string]string{
				"AWS_REGION_PROD_WEST": "us-west-1",
				"AWS_REGION":           "eu-west-1",
				"AWS_DEFAULT_REGION":   "ap-northeast-1",
			},
			want: "us-west-1",
		},
		{
			name:    "AWS_REGION over AWS_DEFAULT_REGION",
			modelID: "anthropic.claude-3-5-sonnet-20241022-v2:0",
			env: map[string]string{
				"AWS_REGION":         "eu-central-1",
				"AWS_DEFAULT_REGION": "us-east-2",
			},
			want: "eu-central-1",
		},
		{
			name:    "AWS_DEFAULT_REGION fallback",
			modelID: "anthropic.claude-3-5-sonnet-20241022-v2:0",
			env: map[string]string{
				"AWS_DEFAULT_REGION": "us-east-2",
			},
			want: "us-east-2",
		},
		{
			name:    "default us-east-1",
			modelID: "anthropic.claude-3-5-sonnet-20241022-v2:0",
			want:    "us-east-1",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := ResolveBedrockRegion(tc.modelID, tc.account, mapGetenv(tc.env))
			if got != tc.want {
				t.Fatalf("ResolveBedrockRegion(%q, %q) = %q, want %q", tc.modelID, tc.account, got, tc.want)
			}
		})
	}
}
