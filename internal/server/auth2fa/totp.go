// Package auth2fa wraps the TOTP and backup-code logic for the user
// security flow. Kept in its own package so the store layer doesn't have
// to know about pquerna/otp internals or QR rendering.
package auth2fa

import (
	"bytes"
	"crypto/rand"
	"crypto/subtle"
	"encoding/base32"
	"encoding/base64"
	"encoding/hex"
	"fmt"
	"image/png"
	"time"

	"github.com/pquerna/otp"
	"github.com/pquerna/otp/totp"
)

// totpPeriod is the TOTP step in seconds (RFC 6238 default).
const totpPeriod = 30

const (
	BackupCodeCount = 10
	Issuer          = "MonSys"
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

// ValidateAndStep is like Validate but, on success, also returns the RFC 6238
// time-step counter the code matched. Callers persist the highest consumed
// step and reject any code whose step is <= it, enforcing single-use OTP
// (ASVS V2.8.4) so a code observed within its ±1-step skew window cannot be
// replayed. It scans the same [-1, 0, +1] skew window totp.Validate uses.
func ValidateAndStep(secretB32, code string) (int64, bool) {
	opts := totp.ValidateOpts{Period: totpPeriod, Skew: 0, Digits: otp.DigitsSix, Algorithm: otp.AlgorithmSHA1}
	base := time.Now().Unix() / totpPeriod
	for _, skew := range []int64{1, 0, -1} {
		step := base + skew
		candidate, err := totp.GenerateCodeCustom(secretB32, time.Unix(step*totpPeriod, 0), opts)
		if err != nil {
			continue
		}
		if subtle.ConstantTimeCompare([]byte(candidate), []byte(code)) == 1 {
			return step, true
		}
	}
	return 0, false
}

// NewBackupCodes returns count cryptographically random codes in the form
// "xxxx-xxxx" (8 hex chars + dash). The dash is purely cosmetic; we strip
// it on validation.
func NewBackupCodes(count int) ([]string, error) {
	out := make([]string, 0, count)
	buf := make([]byte, 4)
	for range count {
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
	for i := range len(s) {
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
