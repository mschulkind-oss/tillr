package crypto

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"errors"
	"fmt"
	"io"
	"strings"
)

// Prefix marks a value as encrypted so we can distinguish from plaintext.
const Prefix = "enc:"

// Encrypt encrypts plaintext using AES-256-GCM and returns a base64-encoded
// ciphertext prefixed with "enc:". The key must be exactly 32 bytes.
func Encrypt(plaintext string, key []byte) (string, error) {
	if len(key) != 32 {
		return "", errors.New("key must be 32 bytes")
	}

	block, err := aes.NewCipher(key)
	if err != nil {
		return "", fmt.Errorf("creating cipher: %w", err)
	}

	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return "", fmt.Errorf("creating GCM: %w", err)
	}

	nonce := make([]byte, gcm.NonceSize())
	if _, err := io.ReadFull(rand.Reader, nonce); err != nil {
		return "", fmt.Errorf("generating nonce: %w", err)
	}

	// Seal appends the ciphertext to the nonce so we can extract it on decrypt.
	sealed := gcm.Seal(nonce, nonce, []byte(plaintext), nil)
	encoded := base64.StdEncoding.EncodeToString(sealed)
	return Prefix + encoded, nil
}

// Decrypt decodes a base64-encoded ciphertext (with or without the "enc:"
// prefix) and decrypts it using AES-256-GCM. The key must be exactly 32 bytes.
func Decrypt(ciphertext string, key []byte) (string, error) {
	if len(key) != 32 {
		return "", errors.New("key must be 32 bytes")
	}

	// Strip the prefix if present.
	ct := strings.TrimPrefix(ciphertext, Prefix)

	data, err := base64.StdEncoding.DecodeString(ct)
	if err != nil {
		return "", fmt.Errorf("decoding base64: %w", err)
	}

	block, err := aes.NewCipher(key)
	if err != nil {
		return "", fmt.Errorf("creating cipher: %w", err)
	}

	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return "", fmt.Errorf("creating GCM: %w", err)
	}

	nonceSize := gcm.NonceSize()
	if len(data) < nonceSize {
		return "", errors.New("ciphertext too short")
	}

	nonce, sealed := data[:nonceSize], data[nonceSize:]
	plaintext, err := gcm.Open(nil, nonce, sealed, nil)
	if err != nil {
		return "", fmt.Errorf("decrypting: %w", err)
	}

	return string(plaintext), nil
}

// DeriveKey produces a 32-byte AES-256 key from an arbitrary password
// using SHA-256. This is intentionally simple — for production use consider
// a proper KDF (scrypt, argon2) but SHA-256 meets the "stdlib only" constraint.
func DeriveKey(password string) []byte {
	h := sha256.Sum256([]byte(password))
	return h[:]
}

// IsEncrypted reports whether value starts with the encryption prefix.
func IsEncrypted(value string) bool {
	return strings.HasPrefix(value, Prefix)
}
