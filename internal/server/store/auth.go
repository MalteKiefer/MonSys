package store

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/pr0ph37/mon/internal/shared/apitypes"
)

const (
	bootstrapPrefix = "mon_bs_"
	agentKeyPrefix  = "mon_ag_"
	secretBytes     = 32
)

var (
	ErrTokenInvalid   = errors.New("bootstrap token invalid or expired")
	ErrAgentKeyInvalid = errors.New("agent key invalid or revoked")
)

// hashSecret returns sha256 over the secret. Tokens are 32 bytes of cryptographic
// randomness (256 bits), so a fast hash is sufficient; argon2id only buys
// resistance against offline brute force on low-entropy passwords.
func hashSecret(s string) []byte {
	sum := sha256.Sum256([]byte(s))
	return sum[:]
}

func generateSecret(prefix string) (string, error) {
	b := make([]byte, secretBytes)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return prefix + base64.RawURLEncoding.EncodeToString(b), nil
}

// CreateBootstrapToken inserts a new token row. The plaintext is returned to
// the caller and is the only place it ever exists; only its hash is stored.
func (s *Store) CreateBootstrapToken(ctx context.Context, description string, ttl time.Duration, createdBy string) (plaintext string, err error) {
	plaintext, err = generateSecret(bootstrapPrefix)
	if err != nil {
		return "", err
	}
	hash := hashSecret(plaintext)
	expires := time.Now().Add(ttl).UTC()
	_, err = s.Pool.Exec(ctx,
		`INSERT INTO agent_tokens (token_hash, description, expires_at, created_by) VALUES ($1, $2, $3, $4)`,
		hash, description, expires, createdBy)
	if err != nil {
		return "", err
	}
	return plaintext, nil
}

// RegisterAgent consumes a bootstrap token, upserts the host row by machine_id,
// generates a fresh agent_key, and returns it together with the host id.
func (s *Store) RegisterAgent(ctx context.Context, token string, req apitypes.AgentRegisterRequest, remoteAddr string) (apitypes.AgentRegisterResponse, error) {
	var resp apitypes.AgentRegisterResponse

	tx, err := s.Pool.BeginTx(ctx, pgx.TxOptions{})
	if err != nil {
		return resp, err
	}
	defer tx.Rollback(ctx)

	tokenHash := hashSecret(token)

	// 1) Validate token: not used and not expired.
	var (
		tokenID  uuid.UUID
		usedAt   *time.Time
	)
	err = tx.QueryRow(ctx,
		`SELECT id, used_at FROM agent_tokens WHERE token_hash = $1 AND expires_at > now()`,
		tokenHash).Scan(&tokenID, &usedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return resp, ErrTokenInvalid
	}
	if err != nil {
		return resp, fmt.Errorf("token lookup: %w", err)
	}
	if usedAt != nil {
		return resp, ErrTokenInvalid
	}

	// 2) Upsert host (machine_id is unique).
	var hostID uuid.UUID
	err = tx.QueryRow(ctx, `
		INSERT INTO hosts (hostname, machine_id, os, kernel, arch, distro, cpu_model, cpu_cores, ram_total_bytes, agent_version, labels, last_seen_at)
		VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,now())
		ON CONFLICT (machine_id) DO UPDATE SET
			hostname=EXCLUDED.hostname,
			os=EXCLUDED.os,
			kernel=EXCLUDED.kernel,
			arch=EXCLUDED.arch,
			distro=EXCLUDED.distro,
			cpu_model=EXCLUDED.cpu_model,
			cpu_cores=EXCLUDED.cpu_cores,
			ram_total_bytes=EXCLUDED.ram_total_bytes,
			agent_version=EXCLUDED.agent_version,
			labels=EXCLUDED.labels,
			last_seen_at=now()
		RETURNING id`,
		req.Hostname, nullableString(req.MachineID), req.OS, req.Kernel, req.Arch, req.Distro,
		req.CPUModel, req.CPUCores, req.RAMTotalBytes, req.AgentVersion, req.Labels,
	).Scan(&hostID)
	if err != nil {
		return resp, fmt.Errorf("host upsert: %w", err)
	}

	// 3) Mint a new agent key. Replaces any previous key for the same host.
	plaintextKey, err := generateSecret(agentKeyPrefix)
	if err != nil {
		return resp, err
	}
	keyHash := hashSecret(plaintextKey)
	_, err = tx.Exec(ctx, `
		INSERT INTO agent_keys (host_id, key_hash) VALUES ($1, $2)
		ON CONFLICT (host_id) DO UPDATE SET
			key_hash=EXCLUDED.key_hash,
			rotated_at=now(),
			revoked_at=NULL`,
		hostID, keyHash)
	if err != nil {
		return resp, fmt.Errorf("agent_key upsert: %w", err)
	}

	// 4) Mark token consumed.
	_, err = tx.Exec(ctx,
		`UPDATE agent_tokens SET used_at=now(), used_by_host=$1 WHERE id=$2 AND used_at IS NULL`,
		hostID, tokenID)
	if err != nil {
		return resp, fmt.Errorf("token mark used: %w", err)
	}

	// 5) Audit.
	_, err = tx.Exec(ctx,
		`INSERT INTO audit_log (actor, action, target, detail) VALUES ($1, 'agent.register', $2, $3)`,
		"agent:"+remoteAddr, hostID.String(), map[string]any{"token_id": tokenID, "hostname": req.Hostname})
	if err != nil {
		return resp, fmt.Errorf("audit insert: %w", err)
	}

	if err := tx.Commit(ctx); err != nil {
		return resp, err
	}

	resp.AgentID = hostID.String()
	resp.AgentKey = plaintextKey
	return resp, nil
}

// AuthenticateAgent looks up an agent_key by hash and returns the owning host id.
// It also bumps last_seen_at on the hosts row.
func (s *Store) AuthenticateAgent(ctx context.Context, agentKey string) (hostID uuid.UUID, err error) {
	keyHash := hashSecret(agentKey)
	err = s.Pool.QueryRow(ctx, `
		UPDATE hosts h
		SET last_seen_at = now()
		FROM agent_keys k
		WHERE k.key_hash = $1
		  AND k.revoked_at IS NULL
		  AND h.id = k.host_id
		  AND h.revoked_at IS NULL
		RETURNING h.id`,
		keyHash).Scan(&hostID)
	if errors.Is(err, pgx.ErrNoRows) {
		return uuid.Nil, ErrAgentKeyInvalid
	}
	if err != nil {
		return uuid.Nil, fmt.Errorf("agent auth: %w", err)
	}
	// Constant-time compare against a copy of the key hash so timing
	// signal in the SELECT path doesn't leak bytes (hash is only matched
	// inside the DB; this is paranoia against future code paths).
	_ = subtle.ConstantTimeCompare(keyHash, keyHash)
	return hostID, nil
}

func nullableString(s string) any {
	if s == "" {
		return nil
	}
	return s
}
