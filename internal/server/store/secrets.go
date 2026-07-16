package store

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"encoding/base64"
	"errors"
	"fmt"
	"os"
	"strings"
	"sync"
)

// AUDIT-2026-07-16 M2: encryption-at-rest for sensitive secrets (TOTP seeds).
//
// The data-encryption key is supplied out-of-band via MON_DATA_ENCRYPTION_KEY
// (base64-encoded 32 bytes for AES-256). It is intentionally optional and the
// scheme is dual-mode so no forced re-encryption / lockout is needed:
//
//   - When a key is set, EncryptAtRest returns a versioned "enc:v1:<b64>"
//     ciphertext; new secrets are stored encrypted.
//   - DecryptAtRest transparently returns legacy plaintext (no prefix) as-is,
//     so rows written before the key was configured keep working.
//
// A mis-set or rotated key only affects values written under the previous key;
// it never corrupts data because plaintext values are never touched.

const encPrefix = "enc:v1:"

var (
	dataKeyOnce sync.Once
	dataKey     []byte // nil when MON_DATA_ENCRYPTION_KEY is unset
	dataKeyErr  error
)

func loadDataKey() ([]byte, error) {
	dataKeyOnce.Do(func() {
		raw := strings.TrimSpace(os.Getenv("MON_DATA_ENCRYPTION_KEY"))
		if raw == "" {
			return
		}
		key, err := base64.StdEncoding.DecodeString(raw)
		if err != nil {
			dataKeyErr = fmt.Errorf("MON_DATA_ENCRYPTION_KEY: not valid base64: %w", err)
			return
		}
		if len(key) != 32 {
			dataKeyErr = fmt.Errorf("MON_DATA_ENCRYPTION_KEY: need 32 bytes (AES-256), got %d", len(key))
			return
		}
		dataKey = key
	})
	return dataKey, dataKeyErr
}

// EncryptionConfigured reports whether an at-rest key is available (and valid).
// main() can call this at startup to fail fast on a malformed key.
func EncryptionConfigured() (bool, error) {
	key, err := loadDataKey()
	return key != nil, err
}

// EncryptAtRest returns a versioned ciphertext when a key is configured, or the
// plaintext unchanged when it is not.
func EncryptAtRest(plaintext string) (string, error) {
	key, err := loadDataKey()
	if err != nil {
		return "", err
	}
	if key == nil {
		return plaintext, nil
	}
	return encryptWithKey(key, plaintext)
}

// DecryptAtRest reverses EncryptAtRest. Values without the versioned prefix are
// treated as legacy plaintext and returned unchanged.
func DecryptAtRest(stored string) (string, error) {
	if !strings.HasPrefix(stored, encPrefix) {
		return stored, nil
	}
	key, err := loadDataKey()
	if err != nil {
		return "", err
	}
	if key == nil {
		return "", errors.New("value is encrypted but MON_DATA_ENCRYPTION_KEY is not set")
	}
	return decryptWithKey(key, stored)
}

func encryptWithKey(key []byte, plaintext string) (string, error) {
	gcm, err := newGCM(key)
	if err != nil {
		return "", err
	}
	nonce := make([]byte, gcm.NonceSize())
	if _, err := rand.Read(nonce); err != nil {
		return "", err
	}
	sealed := gcm.Seal(nonce, nonce, []byte(plaintext), nil)
	return encPrefix + base64.StdEncoding.EncodeToString(sealed), nil
}

func decryptWithKey(key []byte, stored string) (string, error) {
	gcm, err := newGCM(key)
	if err != nil {
		return "", err
	}
	blob, err := base64.StdEncoding.DecodeString(strings.TrimPrefix(stored, encPrefix))
	if err != nil {
		return "", fmt.Errorf("decode ciphertext: %w", err)
	}
	if len(blob) < gcm.NonceSize() {
		return "", errors.New("ciphertext too short")
	}
	nonce, ct := blob[:gcm.NonceSize()], blob[gcm.NonceSize():]
	pt, err := gcm.Open(nil, nonce, ct, nil)
	if err != nil {
		return "", fmt.Errorf("decrypt: %w", err)
	}
	return string(pt), nil
}

func newGCM(key []byte) (cipher.AEAD, error) {
	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, err
	}
	return cipher.NewGCM(block)
}
