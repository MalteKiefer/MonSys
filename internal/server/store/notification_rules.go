package store

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"

	"github.com/pr0ph37/mon/internal/shared/apitypes"
)

var ErrRuleNotFound = errors.New("notification rule not found")

func (s *Store) ListRules(ctx context.Context) ([]apitypes.NotificationRule, error) {
	rows, err := s.Pool.Query(ctx, `
		SELECT id, name, enabled, condition_type, condition_params,
		       channel_ids, severity, throttle_sec, created_at, COALESCE(created_by,'')
		FROM notification_rules ORDER BY name`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []apitypes.NotificationRule{}
	for rows.Next() {
		r, err := scanRule(rows.Scan)
		if err != nil {
			return nil, err
		}
		out = append(out, r)
	}
	return out, rows.Err()
}

func (s *Store) GetRule(ctx context.Context, id uuid.UUID) (apitypes.NotificationRule, error) {
	rows, err := s.Pool.Query(ctx, `
		SELECT id, name, enabled, condition_type, condition_params,
		       channel_ids, severity, throttle_sec, created_at, COALESCE(created_by,'')
		FROM notification_rules WHERE id = $1`, id)
	if err != nil {
		return apitypes.NotificationRule{}, err
	}
	defer rows.Close()
	if !rows.Next() {
		return apitypes.NotificationRule{}, ErrRuleNotFound
	}
	return scanRule(rows.Scan)
}

func (s *Store) CreateRule(ctx context.Context, in apitypes.NotificationRuleInput, createdBy string) (apitypes.NotificationRule, error) {
	if in.Name == "" || in.ConditionType == "" || len(in.ChannelIDs) == 0 {
		return apitypes.NotificationRule{}, errors.New("name, condition_type, channel_ids required")
	}
	channels, err := parseChannelIDs(in.ChannelIDs)
	if err != nil {
		return apitypes.NotificationRule{}, err
	}
	params, err := json.Marshal(orEmptyAny(in.ConditionParams))
	if err != nil {
		return apitypes.NotificationRule{}, err
	}
	severity := in.Severity
	if severity == "" {
		severity = "warning"
	}
	if in.ThrottleSec < 0 {
		in.ThrottleSec = 0
	}

	var id uuid.UUID
	err = s.Pool.QueryRow(ctx, `
		INSERT INTO notification_rules (name, enabled, condition_type, condition_params,
		                                channel_ids, severity, throttle_sec, created_by)
		VALUES ($1,$2,$3,$4,$5,$6,$7,$8)
		RETURNING id`,
		in.Name, in.Enabled, in.ConditionType, params, channels, severity,
		in.ThrottleSec, nullableString(createdBy),
	).Scan(&id)
	if err != nil {
		if pgIsUniqueViolation(err) {
			return apitypes.NotificationRule{}, errors.New("a rule with this name already exists")
		}
		return apitypes.NotificationRule{}, fmt.Errorf("rule insert: %w", err)
	}
	return s.GetRule(ctx, id)
}

func (s *Store) UpdateRule(ctx context.Context, id uuid.UUID, in apitypes.NotificationRuleInput) (apitypes.NotificationRule, error) {
	channels, err := parseChannelIDs(in.ChannelIDs)
	if err != nil {
		return apitypes.NotificationRule{}, err
	}
	params, err := json.Marshal(orEmptyAny(in.ConditionParams))
	if err != nil {
		return apitypes.NotificationRule{}, err
	}
	severity := in.Severity
	if severity == "" {
		severity = "warning"
	}
	tag, err := s.Pool.Exec(ctx, `
		UPDATE notification_rules SET
			name = $2, enabled = $3, condition_type = $4, condition_params = $5,
			channel_ids = $6, severity = $7, throttle_sec = $8
		WHERE id = $1`,
		id, in.Name, in.Enabled, in.ConditionType, params,
		channels, severity, in.ThrottleSec)
	if err != nil {
		if pgIsUniqueViolation(err) {
			return apitypes.NotificationRule{}, errors.New("a rule with this name already exists")
		}
		return apitypes.NotificationRule{}, fmt.Errorf("rule update: %w", err)
	}
	if tag.RowsAffected() == 0 {
		return apitypes.NotificationRule{}, ErrRuleNotFound
	}
	return s.GetRule(ctx, id)
}

func (s *Store) DeleteRule(ctx context.Context, id uuid.UUID) error {
	tag, err := s.Pool.Exec(ctx, `DELETE FROM notification_rules WHERE id = $1`, id)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return ErrRuleNotFound
	}
	return nil
}

// ListAlertHistory returns alerts since the given timestamp.
func (s *Store) ListAlertHistory(ctx context.Context, since time.Time, limit int) ([]apitypes.AlertHistoryEntry, error) {
	if limit <= 0 || limit > 1000 {
		limit = 200
	}
	rows, err := s.Pool.Query(ctx, `
		SELECT id, at, rule_id, COALESCE(rule_name, ''), severity, subject, body,
		       dedup_key, COALESCE(delivered_to, '{}'), delivery_errors
		FROM alert_history
		WHERE at >= $1
		ORDER BY at DESC
		LIMIT $2`, since, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []apitypes.AlertHistoryEntry{}
	for rows.Next() {
		var (
			e         apitypes.AlertHistoryEntry
			ruleID    *uuid.UUID
			errorsRaw []byte
		)
		if err := rows.Scan(&e.ID, &e.At, &ruleID, &e.RuleName, &e.Severity, &e.Subject,
			&e.Body, &e.DedupKey, &e.DeliveredTo, &errorsRaw); err != nil {
			return nil, err
		}
		if ruleID != nil {
			e.RuleID = ruleID.String()
		}
		e.DeliveryErrors = map[string]any{}
		if len(errorsRaw) > 0 {
			_ = json.Unmarshal(errorsRaw, &e.DeliveryErrors)
		}
		out = append(out, e)
	}
	return out, rows.Err()
}

func scanRule(scan func(...any) error) (apitypes.NotificationRule, error) {
	var (
		r           apitypes.NotificationRule
		idVal       uuid.UUID
		paramsRaw   []byte
		channelIDs  []uuid.UUID
	)
	if err := scan(&idVal, &r.Name, &r.Enabled, &r.ConditionType, &paramsRaw,
		&channelIDs, &r.Severity, &r.ThrottleSec, &r.CreatedAt, &r.CreatedBy); err != nil {
		return r, err
	}
	r.ID = idVal.String()
	r.ConditionParams = map[string]any{}
	if len(paramsRaw) > 0 {
		_ = json.Unmarshal(paramsRaw, &r.ConditionParams)
	}
	for _, u := range channelIDs {
		r.ChannelIDs = append(r.ChannelIDs, u.String())
	}
	return r, nil
}

func parseChannelIDs(in []string) ([]uuid.UUID, error) {
	out := make([]uuid.UUID, 0, len(in))
	for _, s := range in {
		u, err := uuid.Parse(s)
		if err != nil {
			return nil, fmt.Errorf("invalid channel id %q: %w", s, err)
		}
		out = append(out, u)
	}
	return out, nil
}
