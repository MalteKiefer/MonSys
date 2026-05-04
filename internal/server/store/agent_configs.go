package store

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/MalteKiefer/MonSys/internal/shared/apitypes"
)

var (
	ErrAgentConfigNotFound = errors.New("agent config not found")
)

// ListAgentConfigs returns every config row, joined with target name when
// the scope is group or host so the UI can render meaningful labels.
func (s *Store) ListAgentConfigs(ctx context.Context) ([]apitypes.AgentConfigEntry, error) {
	rows, err := s.Pool.Query(ctx, `
		SELECT a.id, a.scope, a.target_id, a.config, COALESCE(a.description, ''),
		       a.enabled, a.created_at, a.updated_at, COALESCE(a.updated_by, ''),
		       CASE
		         WHEN a.scope = 'host'  THEN (SELECT hostname FROM hosts WHERE id = a.target_id)
		         WHEN a.scope = 'group' THEN (SELECT name     FROM host_groups WHERE id = a.target_id)
		         ELSE ''
		       END AS target_name
		FROM agent_configs a
		ORDER BY
		  CASE a.scope WHEN 'global' THEN 0 WHEN 'group' THEN 1 ELSE 2 END,
		  target_name`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	out := []apitypes.AgentConfigEntry{}
	for rows.Next() {
		e, err := scanAgentConfig(rows.Scan)
		if err != nil {
			return nil, err
		}
		out = append(out, e)
	}
	return out, rows.Err()
}

func (s *Store) GetAgentConfig(ctx context.Context, id uuid.UUID) (apitypes.AgentConfigEntry, error) {
	rows, err := s.Pool.Query(ctx, `
		SELECT a.id, a.scope, a.target_id, a.config, COALESCE(a.description, ''),
		       a.enabled, a.created_at, a.updated_at, COALESCE(a.updated_by, ''),
		       CASE
		         WHEN a.scope = 'host'  THEN (SELECT hostname FROM hosts WHERE id = a.target_id)
		         WHEN a.scope = 'group' THEN (SELECT name     FROM host_groups WHERE id = a.target_id)
		         ELSE ''
		       END
		FROM agent_configs a WHERE a.id = $1`, id)
	if err != nil {
		return apitypes.AgentConfigEntry{}, err
	}
	defer rows.Close()
	if !rows.Next() {
		return apitypes.AgentConfigEntry{}, ErrAgentConfigNotFound
	}
	return scanAgentConfig(rows.Scan)
}

func (s *Store) UpsertAgentConfig(ctx context.Context, in apitypes.AgentConfigInput, updatedBy string) (apitypes.AgentConfigEntry, error) {
	if err := validateAgentConfigInput(in); err != nil {
		return apitypes.AgentConfigEntry{}, err
	}
	cfg, err := json.Marshal(in.Config)
	if err != nil {
		return apitypes.AgentConfigEntry{}, fmt.Errorf("marshal config: %w", err)
	}

	var targetArg any
	if in.TargetID != "" {
		tid, err := uuid.Parse(in.TargetID)
		if err != nil {
			return apitypes.AgentConfigEntry{}, fmt.Errorf("invalid target_id: %w", err)
		}
		targetArg = tid
	}

	// ON CONFLICT (scope, target_id): we treat (scope, target_id) as the
	// natural key. The unique index in 0011 covers global (NULL), group, and
	// host because Postgres considers two NULLs distinct in a unique index;
	// since global has at most one row we enforce that via a separate guard
	// below.
	if in.Scope == "global" {
		// Refuse a second global row.
		var n int
		if err := s.Pool.QueryRow(ctx,
			`SELECT count(*) FROM agent_configs WHERE scope = 'global'`,
		).Scan(&n); err != nil {
			return apitypes.AgentConfigEntry{}, err
		}
		// Update in place if one already exists; create otherwise.
		if n > 0 {
			var id uuid.UUID
			err := s.Pool.QueryRow(ctx, `
				UPDATE agent_configs SET
					config = $1, description = $2, enabled = $3, updated_at = now(), updated_by = $4
				WHERE scope = 'global' RETURNING id`,
				cfg, nullableString(in.Description), in.Enabled, nullableString(updatedBy),
			).Scan(&id)
			if err != nil {
				return apitypes.AgentConfigEntry{}, fmt.Errorf("update global: %w", err)
			}
			return s.GetAgentConfig(ctx, id)
		}
	}

	var id uuid.UUID
	err = s.Pool.QueryRow(ctx, `
		INSERT INTO agent_configs (scope, target_id, config, description, enabled, updated_by)
		VALUES ($1, $2, $3, $4, $5, $6)
		ON CONFLICT (scope, target_id) DO UPDATE SET
			config = EXCLUDED.config,
			description = EXCLUDED.description,
			enabled = EXCLUDED.enabled,
			updated_at = now(),
			updated_by = EXCLUDED.updated_by
		RETURNING id`,
		in.Scope, targetArg, cfg, nullableString(in.Description), in.Enabled, nullableString(updatedBy),
	).Scan(&id)
	if err != nil {
		return apitypes.AgentConfigEntry{}, fmt.Errorf("upsert agent_config: %w", err)
	}
	return s.GetAgentConfig(ctx, id)
}

func (s *Store) DeleteAgentConfig(ctx context.Context, id uuid.UUID) error {
	tag, err := s.Pool.Exec(ctx, `DELETE FROM agent_configs WHERE id = $1`, id)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return ErrAgentConfigNotFound
	}
	return nil
}

// ResolveAgentConfig walks the precedence chain (host > group > global) and
// merges JSON objects key-wise. The agent receives a single merged config.
func (s *Store) ResolveAgentConfig(ctx context.Context, hostID uuid.UUID) (apitypes.AgentConfigResolved, error) {
	out := apitypes.AgentConfigResolved{FetchedAt: time.Now().UTC()}

	// Collect raw JSON layers in precedence order so we can merge keys
	// host > group > global. Layers later overlay earlier.
	type layer struct {
		raw   []byte
		scope string
	}
	var layers []layer

	// Global.
	var globalRaw []byte
	err := s.Pool.QueryRow(ctx,
		`SELECT config FROM agent_configs WHERE scope = 'global' AND enabled = TRUE`,
	).Scan(&globalRaw)
	if err == nil {
		layers = append(layers, layer{globalRaw, "global"})
	} else if !errors.Is(err, pgx.ErrNoRows) {
		return out, err
	}

	// Group(s) — a host can be in many groups; merge in deterministic order
	// (group name) so the same membership produces the same result.
	groupRows, err := s.Pool.Query(ctx, `
		SELECT a.config, g.name
		FROM agent_configs a
		JOIN host_group_members m ON m.group_id = a.target_id
		JOIN host_groups g ON g.id = a.target_id
		WHERE a.scope = 'group' AND a.enabled = TRUE AND m.host_id = $1
		ORDER BY g.name`, hostID)
	if err != nil {
		return out, err
	}
	for groupRows.Next() {
		var raw []byte
		var name string
		if err := groupRows.Scan(&raw, &name); err != nil {
			groupRows.Close()
			return out, err
		}
		layers = append(layers, layer{raw, "group:" + name})
	}
	groupRows.Close()
	if err := groupRows.Err(); err != nil {
		return out, err
	}

	// Host (most specific).
	var hostRaw []byte
	err = s.Pool.QueryRow(ctx,
		`SELECT config FROM agent_configs WHERE scope = 'host' AND target_id = $1 AND enabled = TRUE`,
		hostID,
	).Scan(&hostRaw)
	if err == nil {
		layers = append(layers, layer{hostRaw, "host"})
	} else if !errors.Is(err, pgx.ErrNoRows) {
		return out, err
	}

	merged := map[string]any{}
	for _, l := range layers {
		if len(l.raw) == 0 {
			continue
		}
		var v map[string]any
		if err := json.Unmarshal(l.raw, &v); err != nil {
			continue
		}
		mergeInto(merged, v)
		out.SourceScopes = append(out.SourceScopes, l.scope)
	}

	// Re-marshal then unmarshal into the typed struct so the response has
	// the same shape regardless of which keys layers contributed.
	if len(merged) > 0 {
		b, _ := json.Marshal(merged)
		_ = json.Unmarshal(b, &out.Config)
	}
	return out, nil
}

// mergeInto deep-merges src onto dst. Maps are merged recursively; everything
// else (slices, scalars) replaces the existing value. This matches operator
// expectations for "overrides extend the parent".
func mergeInto(dst, src map[string]any) {
	for k, v := range src {
		if vMap, ok := v.(map[string]any); ok {
			if existing, ok := dst[k].(map[string]any); ok {
				mergeInto(existing, vMap)
				continue
			}
		}
		dst[k] = v
	}
}

func validateAgentConfigInput(in apitypes.AgentConfigInput) error {
	switch in.Scope {
	case "global":
		if in.TargetID != "" {
			return errors.New("scope=global must not have target_id")
		}
	case "group", "host":
		if in.TargetID == "" {
			return fmt.Errorf("scope=%s requires target_id", in.Scope)
		}
	default:
		return fmt.Errorf("invalid scope: %s", in.Scope)
	}
	return nil
}

func scanAgentConfig(scan func(...any) error) (apitypes.AgentConfigEntry, error) {
	var (
		e        apitypes.AgentConfigEntry
		idVal    uuid.UUID
		targetID *uuid.UUID
		raw      []byte
		target   string
	)
	if err := scan(&idVal, &e.Scope, &targetID, &raw, &e.Description,
		&e.Enabled, &e.CreatedAt, &e.UpdatedAt, &e.UpdatedBy, &target); err != nil {
		return e, err
	}
	e.ID = idVal.String()
	if targetID != nil {
		e.TargetID = targetID.String()
	}
	e.TargetName = target
	if len(raw) > 0 {
		_ = json.Unmarshal(raw, &e.Config)
	}
	return e, nil
}

// HostIDForAgentKey returns the host id owning the given agent key; this is
// used by the agent-config fetch endpoint to identify the caller without
// going through the request middleware (we want agents, not web users).
func (s *Store) HostIDForAgentKey(ctx context.Context, key string) (uuid.UUID, error) {
	keyHash := hashSecret(key)
	var id uuid.UUID
	err := s.Pool.QueryRow(ctx, `
		SELECT h.id FROM hosts h
		JOIN agent_keys k ON k.host_id = h.id
		WHERE k.key_hash = $1 AND k.revoked_at IS NULL AND h.revoked_at IS NULL`,
		keyHash,
	).Scan(&id)
	if errors.Is(err, pgx.ErrNoRows) {
		return uuid.Nil, ErrAgentKeyInvalid
	}
	return id, err
}
