package store

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/pr0ph37/mon/internal/shared/apitypes"
)

// AuditLog inserts a single row into audit_log. detail is a free-form
// description; if it is not already a JSON object/array, it is wrapped as
// {"text":"..."} so the JSONB column accepts it. Errors are returned to the
// caller, but the API layer treats them as best-effort and downgrades them
// to slog warnings — auditing must never block the underlying action.
func (s *Store) AuditLog(ctx context.Context, actor, action, target, detail string) error {
	payload := encodeAuditDetail(detail)
	_, err := s.Pool.Exec(ctx,
		`INSERT INTO audit_log (actor, action, target, detail) VALUES ($1, $2, $3, $4)`,
		nullableString(actor), action, nullableString(target), payload)
	if err != nil {
		return fmt.Errorf("audit insert: %w", err)
	}
	return nil
}

// ListAuditLog returns audit_log rows ordered by at DESC, optionally filtered
// by actor / action substrings (exact match when non-empty). Returns total in
// the matching set when count is requested separately via CountAuditLog.
func (s *Store) ListAuditLog(ctx context.Context, limit, offset int, actor, action string) ([]apitypes.AuditEntry, error) {
	if limit <= 0 {
		limit = 100
	}
	if limit > 500 {
		limit = 500
	}
	if offset < 0 {
		offset = 0
	}

	q := `SELECT id, COALESCE(actor,''), action, COALESCE(target,''), COALESCE(detail::text,''), at
	      FROM audit_log`
	args := []any{}
	conds := []string{}
	if actor != "" {
		args = append(args, actor)
		conds = append(conds, fmt.Sprintf("actor = $%d", len(args)))
	}
	if action != "" {
		args = append(args, action)
		conds = append(conds, fmt.Sprintf("action = $%d", len(args)))
	}
	if len(conds) > 0 {
		q += " WHERE " + strings.Join(conds, " AND ")
	}
	args = append(args, limit)
	args = append(args, offset)
	q += fmt.Sprintf(" ORDER BY at DESC, id DESC LIMIT $%d OFFSET $%d", len(args)-1, len(args))

	rows, err := s.Pool.Query(ctx, q, args...)
	if err != nil {
		return nil, fmt.Errorf("audit list: %w", err)
	}
	defer rows.Close()

	out := make([]apitypes.AuditEntry, 0, limit)
	for rows.Next() {
		var e apitypes.AuditEntry
		if err := rows.Scan(&e.ID, &e.Actor, &e.Action, &e.Target, &e.Detail, &e.At); err != nil {
			return nil, fmt.Errorf("audit scan: %w", err)
		}
		out = append(out, e)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("audit rows: %w", err)
	}
	return out, nil
}

// CountAuditLog returns the total number of rows matching the same filters
// as ListAuditLog. Used by the admin UI to render pagination controls.
func (s *Store) CountAuditLog(ctx context.Context, actor, action string) (int, error) {
	q := `SELECT COUNT(*) FROM audit_log`
	args := []any{}
	conds := []string{}
	if actor != "" {
		args = append(args, actor)
		conds = append(conds, fmt.Sprintf("actor = $%d", len(args)))
	}
	if action != "" {
		args = append(args, action)
		conds = append(conds, fmt.Sprintf("action = $%d", len(args)))
	}
	if len(conds) > 0 {
		q += " WHERE " + strings.Join(conds, " AND ")
	}
	var n int
	if err := s.Pool.QueryRow(ctx, q, args...).Scan(&n); err != nil {
		return 0, fmt.Errorf("audit count: %w", err)
	}
	return n, nil
}

// encodeAuditDetail returns a JSONB-compatible value. A JSON object/array
// passes straight through; any other (or empty) string is wrapped in
// {"text":"..."} so a malformed detail never breaks the insert.
func encodeAuditDetail(detail string) []byte {
	t := strings.TrimSpace(detail)
	if t != "" && (strings.HasPrefix(t, "{") || strings.HasPrefix(t, "[")) {
		// Cheap validity check: only accept it if it parses.
		var raw any
		if err := json.Unmarshal([]byte(t), &raw); err == nil {
			return []byte(t)
		}
	}
	b, _ := json.Marshal(map[string]string{"text": detail})
	return b
}

