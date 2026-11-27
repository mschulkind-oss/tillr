package native

import (
	"bytes"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"net/http"
	"net/url"
	"strings"
	"testing"
	"time"
)

func TestSigV4CanonicalSigningWithoutSessionToken(t *testing.T) {
	payload := []byte(`{"messages":[{"role":"user","content":[{"text":"hello"}]}]}`)
	modelID := "anthropic.claude-3-5-sonnet-20241022-v2:0"
	path, rawPath := EncodeBedrockModelPath(modelID)

	u := &url.URL{
		Scheme:  "https",
		Host:    "bedrock-runtime.us-east-1.amazonaws.com",
		Path:    path,
		RawPath: rawPath,
	}
	req, err := http.NewRequest(http.MethodPost, u.String(), bytes.NewReader(payload))
	if err != nil {
		t.Fatalf("NewRequest: %v", err)
	}
	req.URL = u
	req.Header.Set("Content-Type", "application/json")

	creds := SigV4Credentials{
		AccessKeyID:     "AKIAIOSFODNN7EXAMPLE",
		SecretAccessKey: "wJalrXUtnFEMI/K7MDENG/bPxRfiCYEXAMPLEKEY",
		Region:          "us-east-1",
	}
	ts := time.Date(2026, 11, 27, 12, 34, 56, 0, time.UTC)

	if err := SignSigV4(req, payload, creds, "bedrock", ts); err != nil {
		t.Fatalf("SignSigV4: %v", err)
	}

	if got := req.Header.Get("X-Amz-Date"); got != "20261127T123456Z" {
		t.Fatalf("X-Amz-Date = %q, want 20261127T123456Z", got)
	}
	if got := req.Header.Get("X-Amz-Security-Token"); got != "" {
		t.Fatalf("unexpected X-Amz-Security-Token = %q", got)
	}

	sum := sha256.Sum256(payload)
	wantPayloadHash := hex.EncodeToString(sum[:])
	if got := req.Header.Get("X-Amz-Content-Sha256"); got != wantPayloadHash {
		t.Fatalf("X-Amz-Content-Sha256 = %q, want %q", got, wantPayloadHash)
	}

	wantCanon := strings.Join([]string{
		"POST",
		"/model/anthropic.claude-3-5-sonnet-20241022-v2%253A0/converse-stream",
		"",
		"content-type:application/json\nhost:bedrock-runtime.us-east-1.amazonaws.com\nx-amz-content-sha256:" + wantPayloadHash + "\nx-amz-date:20261127T123456Z\n",
		"content-type;host;x-amz-content-sha256;x-amz-date",
		wantPayloadHash,
	}, "\n")

	canonSum := sha256.Sum256([]byte(wantCanon))
	scope := "20261127/us-east-1/bedrock/aws4_request"
	sts := "AWS4-HMAC-SHA256\n20261127T123456Z\n" + scope + "\n" + hex.EncodeToString(canonSum[:])

	hmacSum := func(key, data []byte) []byte {
		m := hmac.New(sha256.New, key)
		m.Write(data)
		return m.Sum(nil)
	}
	kDate := hmacSum([]byte("AWS4"+creds.SecretAccessKey), []byte("20261127"))
	kRegion := hmacSum(kDate, []byte("us-east-1"))
	kService := hmacSum(kRegion, []byte("bedrock"))
	kSigning := hmacSum(kService, []byte("aws4_request"))
	wantSig := hex.EncodeToString(hmacSum(kSigning, []byte(sts)))

	wantAuth := "AWS4-HMAC-SHA256 Credential=AKIAIOSFODNN7EXAMPLE/" + scope + ", SignedHeaders=content-type;host;x-amz-content-sha256;x-amz-date, Signature=" + wantSig
	if got := req.Header.Get("Authorization"); got != wantAuth {
		t.Fatalf("Authorization mismatch:\n got: %s\nwant: %s", got, wantAuth)
	}
}

func TestSigV4SigningWithSessionToken(t *testing.T) {
	payload := []byte(`{}`)
	path, rawPath := EncodeBedrockModelPath("amazon.nova-pro-v1:0")
	u := &url.URL{
		Scheme:  "https",
		Host:    "bedrock-runtime.us-west-2.amazonaws.com",
		Path:    path,
		RawPath: rawPath,
	}
	req, err := http.NewRequest(http.MethodPost, u.String(), bytes.NewReader(payload))
	if err != nil {
		t.Fatalf("NewRequest: %v", err)
	}
	req.URL = u

	creds := SigV4Credentials{
		AccessKeyID:     "ASIATESTKEY",
		SecretAccessKey: "test-secret-key",
		SessionToken:    "IQoJb3JpZ2luX2VjECoaCXVzLXdlc3QtMiJH",
		Region:          "us-west-2",
	}
	ts := time.Date(2026, 11, 27, 0, 0, 0, 0, time.UTC)

	if err := SignSigV4(req, payload, creds, "", ts); err != nil {
		t.Fatalf("SignSigV4: %v", err)
	}

	if got := req.Header.Get("X-Amz-Security-Token"); got != creds.SessionToken {
		t.Fatalf("X-Amz-Security-Token = %q, want %q", got, creds.SessionToken)
	}
	auth := req.Header.Get("Authorization")
	if !strings.Contains(auth, "SignedHeaders=host;x-amz-content-sha256;x-amz-date;x-amz-security-token,") {
		t.Fatalf("Authorization missing expected signed headers: %s", auth)
	}
	if !strings.Contains(auth, "Credential=ASIATESTKEY/20261127/us-west-2/bedrock/aws4_request,") {
		t.Fatalf("Authorization missing expected scope: %s", auth)
	}
}

func TestSigV4InferenceProfileARNPathEncoding(t *testing.T) {
	arn := "arn:aws:bedrock:us-east-1:123456789012:inference-profile/us.anthropic.claude-3-5-sonnet-20241022-v2:0"
	path, rawPath := EncodeBedrockModelPath(arn)

	wantRaw := "/model/arn%3Aaws%3Abedrock%3Aus-east-1%3A123456789012%3Ainference-profile%2Fus.anthropic.claude-3-5-sonnet-20241022-v2%3A0/converse-stream"
	if rawPath != wantRaw {
		t.Fatalf("rawPath = %q, want %q", rawPath, wantRaw)
	}

	u := &url.URL{
		Scheme:  "https",
		Host:    "bedrock-runtime.us-east-1.amazonaws.com",
		Path:    path,
		RawPath: rawPath,
	}
	if got := canonicalURI(u); got != "/model/arn%253Aaws%253Abedrock%253Aus-east-1%253A123456789012%253Ainference-profile%252Fus.anthropic.claude-3-5-sonnet-20241022-v2%253A0/converse-stream" {
		t.Fatalf("canonicalURI = %q", got)
	}

	req, err := http.NewRequest(http.MethodPost, u.String(), nil)
	if err != nil {
		t.Fatalf("NewRequest: %v", err)
	}
	req.URL = u

	creds := SigV4Credentials{
		AccessKeyID:     "AKIAIOSFODNN7EXAMPLE",
		SecretAccessKey: "wJalrXUtnFEMI/K7MDENG/bPxRfiCYEXAMPLEKEY",
		Region:          "us-east-1",
	}
	if err := SignSigV4(req, nil, creds, "bedrock", time.Date(2026, 11, 27, 1, 2, 3, 0, time.UTC)); err != nil {
		t.Fatalf("SignSigV4: %v", err)
	}
	if !strings.HasPrefix(req.Header.Get("Authorization"), "AWS4-HMAC-SHA256 ") {
		t.Fatalf("unexpected Authorization header: %s", req.Header.Get("Authorization"))
	}
}

func TestSigV4ValidationAndCanonicalHelpers(t *testing.T) {
	if q := canonicalQuery("b=hello%20world&a=1~2&b=abc"); q != "a=1~2&b=abc&b=hello%20world" {
		t.Fatalf("canonicalQuery = %q", q)
	}

	u := &url.URL{Scheme: "https", Host: "example.com", Path: "/"}
	req, _ := http.NewRequest(http.MethodPost, u.String(), nil)

	cases := []struct {
		name  string
		creds SigV4Credentials
	}{
		{"missing access key", SigV4Credentials{SecretAccessKey: "s", Region: "us-east-1"}},
		{"missing secret key", SigV4Credentials{AccessKeyID: "a", Region: "us-east-1"}},
		{"missing region", SigV4Credentials{AccessKeyID: "a", SecretAccessKey: "s"}},
	}
	for _, tc := range cases {
		if err := SignSigV4(req, nil, tc.creds, "bedrock", time.Now()); err == nil {
			t.Errorf("%s: expected error, got nil", tc.name)
		}
	}
}
