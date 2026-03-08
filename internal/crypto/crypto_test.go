package crypto

import (
	"strings"
	"testing"
)

func TestEncryptDecryptRoundTrip(t *testing.T) {
	key := DeriveKey("test-password")
	cases := []string{
		"hello world",
		"sensitive data 🔑",
		"SELECT * FROM users WHERE id = 1",
		strings.Repeat("a", 10000), // large payload
	}

	for _, want := range cases {
		ct, err := Encrypt(want, key)
		if err != nil {
			t.Fatalf("Encrypt(%q): %v", want, err)
		}
		if !IsEncrypted(ct) {
			t.Fatalf("ciphertext missing prefix: %q", ct)
		}

		got, err := Decrypt(ct, key)
		if err != nil {
			t.Fatalf("Decrypt: %v", err)
		}
		if got != want {
			t.Errorf("round-trip mismatch: got %q, want %q", got, want)
		}
	}
}

func TestDecryptWrongKey(t *testing.T) {
	key1 := DeriveKey("password-one")
	key2 := DeriveKey("password-two")

	ct, err := Encrypt("secret", key1)
	if err != nil {
		t.Fatalf("Encrypt: %v", err)
	}

	_, err = Decrypt(ct, key2)
	if err == nil {
		t.Fatal("expected error decrypting with wrong key, got nil")
	}
}

func TestEncryptDecryptEmpty(t *testing.T) {
	key := DeriveKey("key")

	ct, err := Encrypt("", key)
	if err != nil {
		t.Fatalf("Encrypt empty: %v", err)
	}

	got, err := Decrypt(ct, key)
	if err != nil {
		t.Fatalf("Decrypt empty: %v", err)
	}
	if got != "" {
		t.Errorf("expected empty string, got %q", got)
	}
}

func TestIsEncrypted(t *testing.T) {
	tests := []struct {
		value string
		want  bool
	}{
		{"enc:abc123", true},
		{"enc:", true},
		{"hello", false},
		{"", false},
		{"ENC:abc", false}, // case-sensitive
		{"encrypted:abc", false},
	}

	for _, tt := range tests {
		if got := IsEncrypted(tt.value); got != tt.want {
			t.Errorf("IsEncrypted(%q) = %v, want %v", tt.value, got, tt.want)
		}
	}
}

func TestBadKeyLength(t *testing.T) {
	short := []byte("too-short")

	_, err := Encrypt("data", short)
	if err == nil {
		t.Fatal("expected error with short key")
	}

	_, err = Decrypt("enc:AAAA", short)
	if err == nil {
		t.Fatal("expected error with short key")
	}
}

func TestDecryptWithoutPrefix(t *testing.T) {
	key := DeriveKey("pw")

	ct, err := Encrypt("hello", key)
	if err != nil {
		t.Fatal(err)
	}

	// Strip prefix and decrypt — should still work.
	raw := strings.TrimPrefix(ct, Prefix)
	got, err := Decrypt(raw, key)
	if err != nil {
		t.Fatalf("Decrypt without prefix: %v", err)
	}
	if got != "hello" {
		t.Errorf("got %q, want %q", got, "hello")
	}
}

func TestUniqueCiphertexts(t *testing.T) {
	key := DeriveKey("key")
	ct1, _ := Encrypt("same", key)
	ct2, _ := Encrypt("same", key)

	if ct1 == ct2 {
		t.Error("encrypting the same plaintext should produce different ciphertexts (random nonce)")
	}
}
