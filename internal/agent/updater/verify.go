package updater

import (
	"errors"
	"fmt"
	"os"

	minisign "github.com/jedisct1/go-minisign"
)

// publicKeyPlaceholder is the value assigned to PublicKey when no real key
// has been baked into the binary. It must NEVER be a valid minisign public
// key — verifyMinisig hard-fails when it sees this exact string.
const publicKeyPlaceholder = "PLACEHOLDER_MINISIGN_PUBLIC_KEY"

// PublicKey holds the minisign public key embedded into the agent at link
// time. Operators bake the real key in via:
//
//	-ldflags "-X github.com/MalteKiefer/MonSys/internal/agent/updater.PublicKey=$(cat deploy/keys/mon-agent.pub | tail -n1)"
//
// TODO(operator): generate keypair with
//
//	minisign -G -p deploy/keys/mon-agent.pub -s mon-agent.key
//
// and store the secret in the GH Actions repo secret MONSYS_MINISIGN_SECRET_KEY
// (with its password in MONSYS_MINISIGN_PASSWORD). Commit ONLY the .pub file.
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
// reported as (false, err) so the caller can decide whether the legacy
// MON_AGENT_UPDATE_INSECURE path applies.
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

	bin, err := os.ReadFile(binPath) //nolint:gosec // operator-controlled path
	if err != nil {
		return false, fmt.Errorf("read binary: %w", err)
	}

	ok, err := pk.Verify(bin, sig)
	if err != nil {
		return false, fmt.Errorf("verify: %w", err)
	}
	if !ok {
		return false, errors.New("minisign signature does NOT match")
	}
	return true, nil
}
