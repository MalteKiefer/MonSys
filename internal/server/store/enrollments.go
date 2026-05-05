package store

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/MalteKiefer/MonSys/internal/shared/apitypes"
)

var ErrEnrollmentNotFound = errors.New("agent enrollment not found")

const (
	enrollmentTTLDefault = 30
	enrollmentTTLMin     = 5
	enrollmentTTLMax     = 1440
	enrollmentListMax    = 200
	enrollmentListDef    = 50
)

// CreateEnrollment mints a single-use bootstrap token plus the metadata that
// will be applied to the host on its first registration. The plaintext token
// is returned exactly once — only its sha256 is stored.
func (s *Store) CreateEnrollment(ctx context.Context, in apitypes.AgentEnrollmentInput, createdBy string) (apitypes.AgentEnrollment, string, error) {
	var out apitypes.AgentEnrollment

	ttl := in.TTLMinutes
	if ttl <= 0 {
		ttl = enrollmentTTLDefault
	}
	if ttl < enrollmentTTLMin {
		ttl = enrollmentTTLMin
	}
	if ttl > enrollmentTTLMax {
		ttl = enrollmentTTLMax
	}

	tags, err := normalizeTags(in.Tags)
	if err != nil {
		return out, "", err
	}

	groupIDs := make([]uuid.UUID, 0, len(in.GroupIDs))
	groupSeen := map[uuid.UUID]struct{}{}
	for _, raw := range in.GroupIDs {
		id, err := uuid.Parse(raw)
		if err != nil {
			return out, "", fmt.Errorf("invalid group_id %q: %w", raw, err)
		}
		if _, ok := groupSeen[id]; ok {
			continue
		}
		groupSeen[id] = struct{}{}
		groupIDs = append(groupIDs, id)
	}

	plaintext, err := generateSecret(bootstrapPrefix)
	if err != nil {
		return out, "", err
	}
	hash := hashSecret(plaintext)
	expires := time.Now().Add(time.Duration(ttl) * time.Minute).UTC()

	var (
		id        uuid.UUID
		createdAt time.Time
	)
	err = s.Pool.QueryRow(ctx, `
		INSERT INTO agent_tokens (
			token_hash, description, expires_at, created_by,
			default_tags, default_group_ids, default_label
		)
		VALUES ($1, $2, $3, $4, $5, $6, $7)
		RETURNING id, created_at`,
		hash, nullableString(in.Description), expires, nullableString(createdBy),
		tags, groupIDs, nullableString(in.Label),
	).Scan(&id, &createdAt)
	if err != nil {
		return out, "", fmt.Errorf("enrollment insert: %w", err)
	}

	out.ID = id.String()
	out.Label = in.Label
	out.Description = in.Description
	out.Tags = tags
	out.GroupIDs = make([]string, 0, len(groupIDs))
	for _, g := range groupIDs {
		out.GroupIDs = append(out.GroupIDs, g.String())
	}
	out.ExpiresAt = expires
	out.CreatedAt = createdAt
	out.CreatedBy = createdBy
	return out, plaintext, nil
}

// GetEnrollment returns the metadata for a single enrollment row, joining the
// hosts table to surface the consuming host's hostname when present.
func (s *Store) GetEnrollment(ctx context.Context, id uuid.UUID) (apitypes.AgentEnrollment, error) {
	row := s.Pool.QueryRow(ctx, `
		SELECT t.id, t.description, t.default_label, t.default_tags, t.default_group_ids,
		       t.expires_at, t.created_at, t.created_by,
		       t.used_at, t.used_by_host, h.hostname
		FROM agent_tokens t
		LEFT JOIN hosts h ON h.id = t.used_by_host
		WHERE t.id = $1`, id)
	out, err := scanEnrollment(row.Scan)
	if errors.Is(err, pgx.ErrNoRows) {
		return apitypes.AgentEnrollment{}, ErrEnrollmentNotFound
	}
	if err != nil {
		return apitypes.AgentEnrollment{}, fmt.Errorf("enrollment lookup: %w", err)
	}
	return out, nil
}

// ListEnrollments returns enrollments created at or after `since`, newest
// first. limit is clamped to [1, 200] with a default of 50.
func (s *Store) ListEnrollments(ctx context.Context, since time.Time, limit int) ([]apitypes.AgentEnrollment, error) {
	if limit <= 0 {
		limit = enrollmentListDef
	}
	if limit > enrollmentListMax {
		limit = enrollmentListMax
	}
	rows, err := s.Pool.Query(ctx, `
		SELECT t.id, t.description, t.default_label, t.default_tags, t.default_group_ids,
		       t.expires_at, t.created_at, t.created_by,
		       t.used_at, t.used_by_host, h.hostname
		FROM agent_tokens t
		LEFT JOIN hosts h ON h.id = t.used_by_host
		WHERE t.created_at >= $1
		ORDER BY t.created_at DESC
		LIMIT $2`, since, limit)
	if err != nil {
		return nil, fmt.Errorf("enrollment list: %w", err)
	}
	defer rows.Close()

	out := []apitypes.AgentEnrollment{}
	for rows.Next() {
		e, err := scanEnrollment(rows.Scan)
		if err != nil {
			return nil, err
		}
		out = append(out, e)
	}
	return out, rows.Err()
}

// RevokeEnrollment expires an unused enrollment token. Already-consumed rows
// are left untouched and reported as not-found, since revoking them is a
// no-op (the host already exists and has its own agent_key).
func (s *Store) RevokeEnrollment(ctx context.Context, id uuid.UUID) error {
	tag, err := s.Pool.Exec(ctx,
		`UPDATE agent_tokens SET expires_at = now() - interval '1 second'
		 WHERE id = $1 AND used_at IS NULL`, id)
	if err != nil {
		return fmt.Errorf("enrollment revoke: %w", err)
	}
	if tag.RowsAffected() == 0 {
		return ErrEnrollmentNotFound
	}
	return nil
}

func scanEnrollment(scan func(...any) error) (apitypes.AgentEnrollment, error) {
	var (
		out         apitypes.AgentEnrollment
		id          uuid.UUID
		description *string
		label       *string
		tags        []string
		groupIDs    []uuid.UUID
		expiresAt   time.Time
		createdAt   time.Time
		createdBy   *string
		usedAt      *time.Time
		usedByHost  *uuid.UUID
		hostname    *string
	)
	if err := scan(&id, &description, &label, &tags, &groupIDs,
		&expiresAt, &createdAt, &createdBy,
		&usedAt, &usedByHost, &hostname); err != nil {
		return out, err
	}
	out.ID = id.String()
	if description != nil {
		out.Description = *description
	}
	if label != nil {
		out.Label = *label
	}
	if tags == nil {
		out.Tags = []string{}
	} else {
		out.Tags = tags
	}
	out.GroupIDs = make([]string, 0, len(groupIDs))
	for _, g := range groupIDs {
		out.GroupIDs = append(out.GroupIDs, g.String())
	}
	out.ExpiresAt = expiresAt
	out.CreatedAt = createdAt
	if createdBy != nil {
		out.CreatedBy = *createdBy
	}
	out.UsedAt = usedAt
	if usedByHost != nil {
		out.UsedByHostID = usedByHost.String()
	}
	if hostname != nil {
		out.UsedByHostname = *hostname
	}
	return out, nil
}
