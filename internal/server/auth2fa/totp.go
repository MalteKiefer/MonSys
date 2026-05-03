// Package auth2fa wraps the TOTP and backup-code logic for the user
// security flow. Kept in its own package so the store layer doesn't have
// to know about pquerna/otp internals or QR rendering.
package auth2fa

import (
	"bytes"
	"crypto/rand"
	"encoding/base32"
	"encoding/base64"
	"encoding/hex"
	"fmt"
	"image/png"

	"github.com/pquerna/otp/totp"
)

const (
	BackupCodeCount = 10
	Issuer          = "mon"
)

// Generate provisions a fresh TOTP secret, the otpauth:// URL, a PNG QR code
// (base64 — caller wraps in `data:image/png;base64,…` if it wants a data URI),
// and a fresh batch of backup codes.
func Generate(accountName string) (secretB32, otpURL, qrPNGBase64 string, backupCodes []string, err error) {
	key, err := totp.Generate(totp.GenerateOpts{
		Issuer:      Issuer,
		AccountName: accountName,
	})
	if err != nil {
		return "", "", "", nil, fmt.Errorf("totp generate: %w", err)
	}
	secretB32 = key.Secret()
	otpURL = key.URL()

	qr, err := key.Image(256, 256)
	if err != nil {
		return "", "", "", nil, fmt.Errorf("totp qr: %w", err)
	}
	var buf bytes.Buffer
	if err := png.Encode(&buf, qr); err != nil {
		return "", "", "", nil, fmt.Errorf("png encode: %w", err)
	}
	qrPNGBase64 = base64.StdEncoding.EncodeToString(buf.Bytes())

	backupCodes, err = NewBackupCodes(BackupCodeCount)
	if err != nil {
		return "", "", "", nil, err
	}
	return
}

// Validate checks code against secretB32 (raw base32 from the authenticator)
// using default TOTP options: 30s step, SHA1, 6 digits, ±1 step skew —
// pquerna/otp.Validate handles the skew.
func Validate(secretB32, code string) bool {
	return totp.Validate(code, secretB32)
}

// NewBackupCodes returns count cryptographically random codes in the form
// "xxxx-xxxx" (8 hex chars + dash). The dash is purely cosmetic; we strip
// it on validation.
func NewBackupCodes(count int) ([]string, error) {
	out := make([]string, 0, count)
	buf := make([]byte, 4)
	for i := 0; i < count; i++ {
		if _, err := rand.Read(buf); err != nil {
			return nil, err
		}
		h := hex.EncodeToString(buf)
		out = append(out, h[:4]+"-"+h[4:])
	}
	return out, nil
}

// MatchAndConsume looks for code in the slice (with or without dashes); on
// hit, it returns a new slice with that code removed. Caller persists the
// remaining slice.
func MatchAndConsume(codes []string, code string) (remaining []string, ok bool) {
	canon := canonical(code)
	if canon == "" {
		return codes, false
	}
	for i, c := range codes {
		if canonical(c) == canon {
			remaining = make([]string, 0, len(codes)-1)
			remaining = append(remaining, codes[:i]...)
			remaining = append(remaining, codes[i+1:]...)
			return remaining, true
		}
	}
	return codes, false
}

func canonical(s string) string {
	out := make([]byte, 0, len(s))
	for i := 0; i < len(s); i++ {
		c := s[i]
		if c == '-' || c == ' ' {
			continue
		}
		if c >= 'A' && c <= 'Z' {
			c += 'a' - 'A'
		}
		out = append(out, c)
	}
	return string(out)
}

// IsValidB32 reports whether s decodes as a base32 secret. Useful in tests.
func IsValidB32(s string) bool {
	_, err := base32.StdEncoding.WithPadding(base32.NoPadding).DecodeString(s)
	return err == nil
}
