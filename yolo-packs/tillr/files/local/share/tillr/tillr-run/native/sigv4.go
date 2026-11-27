package native

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"sort"
	"strings"
	"time"
)

const (
	sigV4Algorithm  = "AWS4-HMAC-SHA256"
	sigV4Service    = "bedrock"
	sigV4Terminator = "aws4_request"
)

// SigV4Credentials holds the AWS credentials and region used to sign requests.
type SigV4Credentials struct {
	AccessKeyID     string
	SecretAccessKey string
	SessionToken    string
	Region          string
}

// SignSigV4 signs req using AWS Signature Version 4 for the given service
// (defaulting to "bedrock" if empty) at timestamp now (defaulting to UTC now
// if zero). It sets X-Amz-Date, X-Amz-Content-Sha256, optional
// X-Amz-Security-Token, and Authorization on req.Header.
func SignSigV4(req *http.Request, payload []byte, creds SigV4Credentials, service string, now time.Time) error {
	if req == nil || req.URL == nil {
		return errors.New("sigv4: request and URL must be non-nil")
	}
	if strings.TrimSpace(creds.AccessKeyID) == "" {
		return errors.New("sigv4: AccessKeyID is required")
	}
	if strings.TrimSpace(creds.SecretAccessKey) == "" {
		return errors.New("sigv4: SecretAccessKey is required")
	}
	if strings.TrimSpace(creds.Region) == "" {
		return errors.New("sigv4: Region is required")
	}
	if service == "" {
		service = sigV4Service
	}
	if now.IsZero() {
		now = time.Now().UTC()
	} else {
		now = now.UTC()
	}

	if req.Header == nil {
		req.Header = make(http.Header)
	}

	amzDate := now.Format("20060102T150405Z")
	dateStamp := now.Format("20060102")
	payloadHash := sha256Hex(payload)

	req.Header.Set("X-Amz-Date", amzDate)
	req.Header.Set("X-Amz-Content-Sha256", payloadHash)
	if creds.SessionToken != "" {
		req.Header.Set("X-Amz-Security-Token", creds.SessionToken)
	}

	canonicalReq, signedHeaders := buildCanonicalRequest(req, payloadHash)
	scope := fmt.Sprintf("%s/%s/%s/%s", dateStamp, creds.Region, service, sigV4Terminator)
	stringToSign := strings.Join([]string{
		sigV4Algorithm,
		amzDate,
		scope,
		sha256Hex([]byte(canonicalReq)),
	}, "\n")

	signingKey := deriveSigV4Key(creds.SecretAccessKey, dateStamp, creds.Region, service)
	signature := hex.EncodeToString(hmacSHA256(signingKey, []byte(stringToSign)))

	authHeader := fmt.Sprintf(
		"%s Credential=%s/%s, SignedHeaders=%s, Signature=%s",
		sigV4Algorithm,
		creds.AccessKeyID,
		scope,
		signedHeaders,
		signature,
	)
	req.Header.Set("Authorization", authHeader)
	return nil
}

// EncodeBedrockModelPath returns the unescaped path and RFC-3986-escaped rawPath
// for a Bedrock ConverseStream request, properly encoding colons and slashes in
// model IDs and inference-profile ARNs.
func EncodeBedrockModelPath(modelID string) (path string, rawPath string) {
	path = "/model/" + modelID + "/converse-stream"
	rawPath = "/model/" + awsURIEscape(modelID, true) + "/converse-stream"
	return path, rawPath
}

func buildCanonicalRequest(req *http.Request, payloadHash string) (canonical string, signedHeaders string) {
	method := req.Method
	if method == "" {
		method = http.MethodGet
	}
	canonURI := canonicalURI(req.URL)
	canonQuery := canonicalQuery(req.URL.RawQuery)
	canonHeaders, signedHeaders := canonicalHeaders(req)

	canonical = strings.Join([]string{
		method,
		canonURI,
		canonQuery,
		canonHeaders,
		signedHeaders,
		payloadHash,
	}, "\n")
	return canonical, signedHeaders
}

// canonicalURI implements AWS SigV4 URI canonicalization for non-S3 services:
// each path segment is URI-encoded twice (i.e. EscapedPath is RFC-3986 escaped
// without escaping '/').
func canonicalURI(u *url.URL) string {
	escaped := u.EscapedPath()
	if escaped == "" {
		return "/"
	}
	return awsURIEscape(escaped, false)
}

func canonicalQuery(rawQuery string) string {
	if rawQuery == "" {
		return ""
	}
	values, err := url.ParseQuery(rawQuery)
	if err != nil {
		return ""
	}
	keys := make([]string, 0, len(values))
	for k := range values {
		keys = append(keys, k)
	}
	sort.Strings(keys)

	var parts []string
	for _, k := range keys {
		ek := awsURIEscape(k, true)
		vals := append([]string(nil), values[k]...)
		sort.Strings(vals)
		for _, v := range vals {
			parts = append(parts, ek+"="+awsURIEscape(v, true))
		}
	}
	return strings.Join(parts, "&")
}

func canonicalHeaders(req *http.Request) (canon string, signed string) {
	merged := make(map[string][]string, len(req.Header)+1)
	for k, vals := range req.Header {
		lk := strings.ToLower(strings.TrimSpace(k))
		if lk == "" {
			continue
		}
		for _, v := range vals {
			merged[lk] = append(merged[lk], normalizeHeaderValue(v))
		}
	}
	host := req.Host
	if host == "" && req.URL != nil {
		host = req.URL.Host
	}
	if host != "" {
		merged["host"] = []string{normalizeHeaderValue(host)}
	}

	keys := make([]string, 0, len(merged))
	for k := range merged {
		keys = append(keys, k)
	}
	sort.Strings(keys)

	var b strings.Builder
	for _, k := range keys {
		b.WriteString(k)
		b.WriteByte(':')
		b.WriteString(strings.Join(merged[k], ","))
		b.WriteByte('\n')
	}
	return b.String(), strings.Join(keys, ";")
}

func normalizeHeaderValue(v string) string {
	return strings.Join(strings.Fields(strings.TrimSpace(v)), " ")
}

// awsURIEscape encodes s according to RFC 3986 unreserved characters
// (A-Z, a-z, 0-9, '-', '_', '.', '~'). When encodeSlash is false, '/' is left
// unescaped.
func awsURIEscape(s string, encodeSlash bool) string {
	var b strings.Builder
	b.Grow(len(s))
	const hexDigits = "0123456789ABCDEF"
	for i := 0; i < len(s); i++ {
		c := s[i]
		if (c >= 'A' && c <= 'Z') ||
			(c >= 'a' && c <= 'z') ||
			(c >= '0' && c <= '9') ||
			c == '-' || c == '_' || c == '.' || c == '~' ||
			(!encodeSlash && c == '/') {
			b.WriteByte(c)
		} else {
			b.WriteByte('%')
			b.WriteByte(hexDigits[c>>4])
			b.WriteByte(hexDigits[c&0x0F])
		}
	}
	return b.String()
}

func deriveSigV4Key(secret, dateStamp, region, service string) []byte {
	kDate := hmacSHA256([]byte("AWS4"+secret), []byte(dateStamp))
	kRegion := hmacSHA256(kDate, []byte(region))
	kService := hmacSHA256(kRegion, []byte(service))
	return hmacSHA256(kService, []byte(sigV4Terminator))
}

func hmacSHA256(key, data []byte) []byte {
	mac := hmac.New(sha256.New, key)
	mac.Write(data)
	return mac.Sum(nil)
}

func sha256Hex(data []byte) string {
	sum := sha256.Sum256(data)
	return hex.EncodeToString(sum[:])
}
