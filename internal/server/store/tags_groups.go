package store

import (
	"context"
	"errors"
	"fmt"
	"regexp"
	"strings"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/pr0ph37/mon/internal/shared/apitypes"
)

var (
	ErrGroupNotFound = errors.New("host group not found")
	ErrInvalidTag    = errors.New("tag must match [a-z0-9][a-z0-9_-]*")
)

var tagRE = regexp.MustCompile(`^[a-z0-9][a-z0-9_-]{0,62}$`)

// ReplaceHostTags wipes the existing tag set for a host and inserts the new
// list. Tags are normalized to lowercase and validated. Caller passes raw
// strings; we deduplicate.
func (s *Store) ReplaceHostTags(ctx context.Context, hostID uuid.UUID, tags []string) error {
	cleaned, err := normalizeTags(tags)
	if err != nil {
		return err
	}
	tx, err := s.Pool.BeginTx(ctx, pgx.TxOptions{})
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)

	var ok bool
	if err := tx.QueryRow(ctx, `SELECT EXISTS (SELECT 1 FROM hosts WHERE id = $1)`, hostID).Scan(&ok); err != nil {
		return err
	}
	if !ok {
		return ErrHostNotFound
	}

	if _, err := tx.Exec(ctx, `DELETE FROM host_tags WHERE host_id = $1`, hostID); err != nil {
		return err
	}
	for _, t := range cleaned {
		if _, err := tx.Exec(ctx,
			`INSERT INTO host_tags (host_id, tag) VALUES ($1, $2) ON CONFLICT DO NOTHING`,
			hostID, t); err != nil {
			return err
		}
	}
	return tx.Commit(ctx)
}

func normalizeTags(in []string) ([]string, error) {
	seen := map[string]struct{}{}
	out := []string{}
	for _, raw := range in {
		t := strings.ToLower(strings.TrimSpace(raw))
		if t == "" {
			continue
		}
		if !tagRE.MatchString(t) {
			return nil, fmt.Errorf("%w: %q", ErrInvalidTag, t)
		}
		if _, ok := seen[t]; ok {
			continue
		}
		seen[t] = struct{}{}
		out = append(out, t)
	}
	return out, nil
}

// ListAllTags returns every distinct tag in use, with the count of hosts
// carrying it. Useful for tag pickers.
type TagUsage struct {
	Tag   string
	Count int
}

func (s *Store) ListAllTags(ctx context.Context) ([]TagUsage, error) {
	rows, err := s.Pool.Query(ctx,
		`SELECT tag, count(*)::int FROM host_tags GROUP BY tag ORDER BY tag`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []TagUsage{}
	for rows.Next() {
		var t TagUsage
		if err := rows.Scan(&t.Tag, &t.Count); err != nil {
			return nil, err
		}
		out = append(out, t)
	}
	return out, rows.Err()
}

// --- Groups ---------------------------------------------------------------

func (s *Store) ListGroups(ctx context.Context) ([]apitypes.HostGroup, error) {
	rows, err := s.Pool.Query(ctx, `
		SELECT g.id, g.name, COALESCE(g.description, ''),
		       g.created_at, COALESCE(g.created_by, ''),
		       COALESCE(array_agg(m.host_id) FILTER (WHERE m.host_id IS NOT NULL), '{}')
		FROM host_groups g
		LEFT JOIN host_group_members m ON m.group_id = g.id
		GROUP BY g.id ORDER BY g.name`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []apitypes.HostGroup{}
	for rows.Next() {
		g, err := scanGroup(rows.Scan)
		if err != nil {
			return nil, err
		}
		out = append(out, g)
	}
	return out, rows.Err()
}

func (s *Store) GetGroup(ctx context.Context, id uuid.UUID) (apitypes.HostGroup, error) {
	rows, err := s.Pool.Query(ctx, `
		SELECT g.id, g.name, COALESCE(g.description, ''),
		       g.created_at, COALESCE(g.created_by, ''),
		       COALESCE(array_agg(m.host_id) FILTER (WHERE m.host_id IS NOT NULL), '{}')
		FROM host_groups g
		LEFT JOIN host_group_members m ON m.group_id = g.id
		WHERE g.id = $1
		GROUP BY g.id`, id)
	if err != nil {
		return apitypes.HostGroup{}, err
	}
	defer rows.Close()
	if !rows.Next() {
		return apitypes.HostGroup{}, ErrGroupNotFound
	}
	return scanGroup(rows.Scan)
}

func (s *Store) CreateGroup(ctx context.Context, in apitypes.HostGroupInput, createdBy string) (apitypes.HostGroup, error) {
	if strings.TrimSpace(in.Name) == "" {
		return apitypes.HostGroup{}, errors.New("name required")
	}
	var id uuid.UUID
	err := s.Pool.QueryRow(ctx, `
		INSERT INTO host_groups (name, description, created_by)
		VALUES ($1, $2, $3) RETURNING id`,
		in.Name, nullableString(in.Description), nullableString(createdBy),
	).Scan(&id)
	if err != nil {
		if pgIsUniqueViolation(err) {
			return apitypes.HostGroup{}, errors.New("a group with this name already exists")
		}
		return apitypes.HostGroup{}, fmt.Errorf("group insert: %w", err)
	}
	return s.GetGroup(ctx, id)
}

func (s *Store) UpdateGroup(ctx context.Context, id uuid.UUID, in apitypes.HostGroupInput) (apitypes.HostGroup, error) {
	tag, err := s.Pool.Exec(ctx,
		`UPDATE host_groups SET name = $2, description = $3 WHERE id = $1`,
		id, in.Name, nullableString(in.Description))
	if err != nil {
		if pgIsUniqueViolation(err) {
			return apitypes.HostGroup{}, errors.New("a group with this name already exists")
		}
		return apitypes.HostGroup{}, fmt.Errorf("group update: %w", err)
	}
	if tag.RowsAffected() == 0 {
		return apitypes.HostGroup{}, ErrGroupNotFound
	}
	return s.GetGroup(ctx, id)
}

func (s *Store) DeleteGroup(ctx context.Context, id uuid.UUID) error {
	tag, err := s.Pool.Exec(ctx, `DELETE FROM host_groups WHERE id = $1`, id)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return ErrGroupNotFound
	}
	return nil
}

// ReplaceGroupMembers wipes and re-inserts the membership for a group.
func (s *Store) ReplaceGroupMembers(ctx context.Context, groupID uuid.UUID, hostIDs []uuid.UUID) error {
	tx, err := s.Pool.BeginTx(ctx, pgx.TxOptions{})
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)

	var ok bool
	if err := tx.QueryRow(ctx, `SELECT EXISTS (SELECT 1 FROM host_groups WHERE id = $1)`, groupID).Scan(&ok); err != nil {
		return err
	}
	if !ok {
		return ErrGroupNotFound
	}

	if _, err := tx.Exec(ctx, `DELETE FROM host_group_members WHERE group_id = $1`, groupID); err != nil {
		return err
	}
	for _, h := range hostIDs {
		if _, err := tx.Exec(ctx,
			`INSERT INTO host_group_members (group_id, host_id) VALUES ($1, $2) ON CONFLICT DO NOTHING`,
			groupID, h); err != nil {
			return err
		}
	}
	return tx.Commit(ctx)
}

func scanGroup(scan func(...any) error) (apitypes.HostGroup, error) {
	var (
		g       apitypes.HostGroup
		idVal   uuid.UUID
		members []uuid.UUID
	)
	if err := scan(&idVal, &g.Name, &g.Description, &g.CreatedAt, &g.CreatedBy, &members); err != nil {
		return g, err
	}
	g.ID = idVal.String()
	g.MemberIDs = make([]string, 0, len(members))
	for _, m := range members {
		g.MemberIDs = append(g.MemberIDs, m.String())
	}
	return g, nil
}
