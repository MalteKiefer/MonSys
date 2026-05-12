package updater

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"os"

	minisign "github.com/jedisct1/go-minisign"
)

// publicKeyPlaceholder is the value assigned to PublicKey when no real key
// has been baked into the binary. It must NEVER be a valid minisign public
// key — verifyMinisig hard-fails when it sees this exact string.
const publicKeyPlaceholder = "PLACEHOLDER_MINISIGN_PUBLIC_KEY"

// PublicKey holds the minisign public key used to authenticate downloaded
// agent binaries. It is the trust root for the self-update flow.
//
// # Trust-root sourcing
//
// PublicKey is set in one of two ways, both of which must be controlled by
// the operator — not by anything the server or the manifest provides:
//
//  1. Linker-injected at build time via:
//
//     -ldflags "-X github.com/MalteKiefer/MonSys/internal/agent/updater.PublicKey=$(cat deploy/keys/mon-agent.pub | tail -n1)"
//
//     This is the default in release builds. The .pub file is committed to
//     the repository; the matching secret lives in the GitHub Actions
//     secrets MONSYS_MINISIGN_SECRET_KEY / MONSYS_MINISIGN_PASSWORD.
//
//  2. (Future) loaded at startup from a file path the operator controls,
//     e.g. /etc/mon-agent/minisign.pub. The Run options would have to grow
//     a PublicKeyPath field; today the linker injection is the only
//     supported path.
//
// What is NOT allowed: the public key MUST NOT be read from the manifest,
// from a sidecar HTTP endpoint, or from any other server-supplied source.
// Doing so would be self-defeating — an attacker who compromised the
// update server could ship their own key alongside their own signed
// binary. The whole point of minisign here is that the trust anchor
// predates the network fetch.
//
// The keypair was generated with:
//
//	minisign -G -p deploy/keys/mon-agent.pub -s mon-agent.key
//
//nolint:gochecknoglobals // Linker-injected at build time.
var PublicKey = publicKeyPlaceholder

// errVerificationDisabled is returned by verifyMinisig when no real key has
// been embedded. The updater treats this as a hard fail unless the operator
// has set MON_AGENT_UPDATE_INSECURE=1 (loud emergency override).
var errVerificationDisabled = errors.New("minisign verification disabled: no public key embedded (build with -X updater.PublicKey=...)")

// verifyMinisig verifies that sigPath is a valid minisign signature over
// binPath produced by the holder of the secret key matching pubKey.
//
// It returns (true, nil) only on cryptographic success. Any error path —
// missing key, malformed signature, mismatched key id, bad signature — is
// reported as (false, err) so the caller can decide whether the
// MON_AGENT_UPDATE_INSECURE path applies.
//
// The whole binary is read into memory BEFORE being passed to minisign.
// This is intentional: minisign verifies a digest over the entire payload,
// so a partial read would either fail verification or — worse, if the
// signature were ever to cover only a prefix — let through a truncated
// binary. Reading via io.ReadAll on an *os.File guarantees we either
// observe the whole file or surface the I/O error.
func verifyMinisig(binPath, sigPath, pubKey string) (bool, error) {
	if pubKey == "" || pubKey == publicKeyPlaceholder {
		return false, errVerificationDisabled
	}

	pk, err := minisign.NewPublicKey(pubKey)
	if err != nil {
		return false, fmt.Errorf("parse public key: %w", err)
	}

	sigBytes, err := os.ReadFile(sigPath) //nolint:gosec // operator-controlled path
	if err != nil {
		return false, fmt.Errorf("read signature: %w", err)
	}
	sig, err := minisign.DecodeSignature(string(sigBytes))
	if err != nil {
		return false, fmt.Errorf("decode signature: %w", err)
	}

	bin, contentHash, err := readAllWithHash(binPath)
	if err != nil {
		return false, fmt.Errorf("read binary: %w", err)
	}

	ok, err := pk.Verify(bin, sig)
	if err != nil {
		return false, fmt.Errorf("verify (sha256=%s): %w", contentHash, err)
	}
	if !ok {
		return false, fmt.Errorf("minisign signature does NOT match (sha256=%s)", contentHash)
	}
	return true, nil
}

// readAllWithHash reads the entire file at path and computes its SHA-256
// in one pass. The hash is returned for diagnostic logging only —
// authentication is provided by minisign over the same byte stream.
//
// We deliberately use io.Copy over a multiwriter, not os.ReadFile + a
// separate hash pass, so the bytes-read-and-the-bytes-hashed are
// guaranteed to be the same view of the file (no two-stat race).
func readAllWithHash(path string) ([]byte, string, error) {
	f, err := os.Open(path) //nolint:gosec // operator-controlled path
	if err != nil {
		return nil, "", err
	}
	defer f.Close()

	h := sha256.New()
	// Use a multi-writer pattern via TeeReader so we hash and buffer in
	// one pass. io.ReadAll on the tee'd reader fully drains the file.
	buf, err := io.ReadAll(io.TeeReader(f, h))
	if err != nil {
		return nil, "", err
	}
	return buf, hex.EncodeToString(h.Sum(nil)), nil
}
