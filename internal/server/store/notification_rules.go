package store

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"regexp"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/MalteKiefer/MonSys/internal/shared/apitypes"
)

// auditActionPatternMaxLen mirrors the maxLength tag on
// apitypes.AuditActionParams.{ActorPattern,TargetPattern}. Kept in sync by
// hand because Huma reflection happens at boot; this constant is what we
// actually enforce in CreateRule/UpdateRule/CreateRuleGroup.
const auditActionPatternMaxLen = 256

// validateAuditActionParams rejects audit_action rules whose actor_pattern or
// target_pattern are too long or fail to compile under Go's regexp (RE2)
// engine. Catastrophic-backtracking constructs ((a+)+, nested unbounded
// quantifiers, …) that Postgres' POSIX `~` engine would happily try to
// evaluate are rejected here so they never reach the alerts engine query
// path. The alerts engine separately wraps the audit query in a 2s
// statement_timeout as a defence-in-depth measure.
func validateAuditActionParams(params map[string]any) error {
	if params == nil {
		return nil
	}
	for _, key := range []string{"actor_pattern", "target_pattern"} {
		raw, ok := params[key]
		if !ok {
			continue
		}
		pat, ok := raw.(string)
		if !ok {
			return fmt.Errorf("%s must be a string", key)
		}
		if pat == "" {
			continue
		}
		if len(pat) > auditActionPatternMaxLen {
			return fmt.Errorf("%s exceeds %d-character limit", key, auditActionPatternMaxLen)
		}
		if _, err := regexp.Compile(pat); err != nil {
			return fmt.Errorf("%s is not a valid regex: %w", key, err)
		}
	}
	return nil
}

var ErrRuleNotFound = errors.New("notification rule not found")

func (s *Store) ListRules(ctx context.Context) ([]apitypes.NotificationRule, error) {
	rows, err := s.Pool.Query(ctx, `
		SELECT id, name, enabled, condition_type, condition_params,
		       channel_ids, severity, throttle_sec,
		       repeat_interval_sec, notify_on_resolve,
		       target_host_ids, target_tags, target_group_ids,
		       group_id,
		       created_at, COALESCE(created_by,'')
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
		       channel_ids, severity, throttle_sec,
		       repeat_interval_sec, notify_on_resolve,
		       target_host_ids, target_tags, target_group_ids,
		       group_id,
		       created_at, COALESCE(created_by,'')
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
	if in.ConditionType == apitypes.ConditionAuditAction {
		if err := validateAuditActionParams(in.ConditionParams); err != nil {
			return apitypes.NotificationRule{}, err
		}
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
	if err := validateRepeat(in.RepeatIntervalSec); err != nil {
		return apitypes.NotificationRule{}, err
	}

	hostIDs, err := parseChannelIDs(in.TargetHostIDs)
	if err != nil {
		return apitypes.NotificationRule{}, fmt.Errorf("target_host_ids: %w", err)
	}
	groupIDs, err := parseChannelIDs(in.TargetGroupIDs)
	if err != nil {
		return apitypes.NotificationRule{}, fmt.Errorf("target_group_ids: %w", err)
	}
	tags := orEmptyStrings(in.TargetTags)

	var id uuid.UUID
	err = s.Pool.QueryRow(ctx, `
		INSERT INTO notification_rules (name, enabled, condition_type, condition_params,
		                                channel_ids, severity, throttle_sec,
		                                repeat_interval_sec, notify_on_resolve,
		                                target_host_ids, target_tags, target_group_ids,
		                                group_id, created_by)
		VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13,$14)
		RETURNING id`,
		in.Name, in.Enabled, in.ConditionType, params, channels, severity,
		in.ThrottleSec, in.RepeatIntervalSec, in.NotifyOnResolve,
		hostIDs, tags, groupIDs, nullableUUID(in.GroupID), nullableString(createdBy),
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
	if in.ConditionType == apitypes.ConditionAuditAction {
		if err := validateAuditActionParams(in.ConditionParams); err != nil {
			return apitypes.NotificationRule{}, err
		}
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
	if err := validateRepeat(in.RepeatIntervalSec); err != nil {
		return apitypes.NotificationRule{}, err
	}
	hostIDs, err := parseChannelIDs(in.TargetHostIDs)
	if err != nil {
		return apitypes.NotificationRule{}, fmt.Errorf("target_host_ids: %w", err)
	}
	groupIDs, err := parseChannelIDs(in.TargetGroupIDs)
	if err != nil {
		return apitypes.NotificationRule{}, fmt.Errorf("target_group_ids: %w", err)
	}
	tags := orEmptyStrings(in.TargetTags)

	tag, err := s.Pool.Exec(ctx, `
		UPDATE notification_rules SET
			name = $2, enabled = $3, condition_type = $4, condition_params = $5,
			channel_ids = $6, severity = $7, throttle_sec = $8,
			repeat_interval_sec = $9, notify_on_resolve = $10,
			target_host_ids = $11, target_tags = $12, target_group_ids = $13,
			group_id = $14
		WHERE id = $1`,
		id, in.Name, in.Enabled, in.ConditionType, params,
		channels, severity, in.ThrottleSec,
		in.RepeatIntervalSec, in.NotifyOnResolve,
		hostIDs, tags, groupIDs, nullableUUID(in.GroupID))
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
//
// restrictedToChannels limits the result to alerts where at least one of the
// caller's channel UUIDs appears in delivered_to (Postgres array overlap).
// Pass nil for the unfiltered admin view; an empty (non-nil) slice means
// "the caller owns no channels" and yields zero rows. Channel UUIDs are
// stringified — alert_history.delivered_to stores them as text.
func (s *Store) ListAlertHistory(ctx context.Context, since time.Time, limit int, restrictedToChannels []uuid.UUID) ([]apitypes.AlertHistoryEntry, error) {
	if limit <= 0 || limit > 1000 {
		limit = 200
	}
	// Caller has no channels at all → short-circuit; the SQL would otherwise
	// need an empty array literal which pgx struggles to type-infer.
	if restrictedToChannels != nil && len(restrictedToChannels) == 0 {
		return []apitypes.AlertHistoryEntry{}, nil
	}
	var (
		rows pgx.Rows
		err  error
	)
	if restrictedToChannels == nil {
		rows, err = s.Pool.Query(ctx, `
			SELECT id, at, rule_id, COALESCE(rule_name, ''), severity, subject, body,
			       dedup_key, COALESCE(delivered_to, '{}'), delivery_errors
			FROM alert_history
			WHERE at >= $1
			ORDER BY at DESC
			LIMIT $2`, since, limit)
	} else {
		channelStrs := make([]string, len(restrictedToChannels))
		for i, c := range restrictedToChannels {
			channelStrs[i] = c.String()
		}
		rows, err = s.Pool.Query(ctx, `
			SELECT id, at, rule_id, COALESCE(rule_name, ''), severity, subject, body,
			       dedup_key, COALESCE(delivered_to, '{}'), delivery_errors
			FROM alert_history
			WHERE at >= $1
			  AND delivered_to && $3::text[]
			ORDER BY at DESC
			LIMIT $2`, since, limit, channelStrs)
	}
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
		r          apitypes.NotificationRule
		idVal      uuid.UUID
		paramsRaw  []byte
		channelIDs []uuid.UUID
		hostIDs    []uuid.UUID
		groupIDs   []uuid.UUID
		tags       []string
		groupID    *uuid.UUID
	)
	if err := scan(&idVal, &r.Name, &r.Enabled, &r.ConditionType, &paramsRaw,
		&channelIDs, &r.Severity, &r.ThrottleSec,
		&r.RepeatIntervalSec, &r.NotifyOnResolve,
		&hostIDs, &tags, &groupIDs,
		&groupID,
		&r.CreatedAt, &r.CreatedBy); err != nil {
		return r, err
	}
	r.GroupID = groupID
	r.ID = idVal.String()
	r.ConditionParams = map[string]any{}
	if len(paramsRaw) > 0 {
		if err := json.Unmarshal(paramsRaw, &r.ConditionParams); err != nil {
			slog.Warn("notification_rules: corrupt config jsonb", "rule_id", r.ID, "err", err)
			r.ConditionParams = map[string]any{}
		}
	}
	r.ChannelIDs = []string{}
	for _, u := range channelIDs {
		r.ChannelIDs = append(r.ChannelIDs, u.String())
	}
	r.TargetHostIDs = []string{}
	for _, u := range hostIDs {
		r.TargetHostIDs = append(r.TargetHostIDs, u.String())
	}
	r.TargetGroupIDs = []string{}
	for _, u := range groupIDs {
		r.TargetGroupIDs = append(r.TargetGroupIDs, u.String())
	}
	r.TargetTags = tags
	if r.TargetTags == nil {
		r.TargetTags = []string{}
	}
	return r, nil
}

// validateRepeat enforces the same range as the migration's CHECK constraint
// so the API surface returns a friendly 400 instead of a raw constraint
// violation. 0 = "fire once per outage", 60..86400 = reminder cadence.
func validateRepeat(secs int) error {
	if secs == 0 {
		return nil
	}
	if secs < 60 || secs > 86400 {
		return errors.New("repeat_interval_sec must be 0 or between 60 and 86400")
	}
	return nil
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

// CreateRuleGroup creates N notification_rules in one transaction. All share
// the same group_id (newly generated), name, scope, and notify config; each
// gets its own condition_type / condition_params. Returns the group_id and
// the created rules. UNIQUE(name) on notification_rules forces us to suffix
// per-row names with the condition_type when N > 1; the shared group_id is
// what the UI uses to roll them back into a single logical rule.
func (s *Store) CreateRuleGroup(ctx context.Context, in apitypes.NotificationRuleGroupInput, createdBy string) (apitypes.NotificationRuleGroupResponse, error) {
	if len(in.Conditions) == 0 {
		return apitypes.NotificationRuleGroupResponse{}, errors.New("at least one condition required")
	}
	if in.Name == "" {
		return apitypes.NotificationRuleGroupResponse{}, errors.New("name required")
	}
	if len(in.ChannelIDs) == 0 {
		return apitypes.NotificationRuleGroupResponse{}, errors.New("at least one channel required")
	}
	if in.ThrottleSec < 0 {
		in.ThrottleSec = 0
	}
	if err := validateRepeat(in.RepeatIntervalSec); err != nil {
		return apitypes.NotificationRuleGroupResponse{}, err
	}

	sev := in.Severity
	if sev == "" {
		sev = "warning"
	}
	groupID := uuid.New()

	// Normalize array inputs to non-nil so pgx encodes empty arrays correctly.
	channels := in.ChannelIDs
	if channels == nil {
		channels = []uuid.UUID{}
	}
	hostIDs := in.TargetHostIDs
	if hostIDs == nil {
		hostIDs = []uuid.UUID{}
	}
	tgtGroups := in.TargetGroupIDs
	if tgtGroups == nil {
		tgtGroups = []uuid.UUID{}
	}
	tags := orEmptyStrings(in.TargetTags)

	tx, err := s.Pool.BeginTx(ctx, pgx.TxOptions{})
	if err != nil {
		return apitypes.NotificationRuleGroupResponse{}, err
	}
	defer func() { _ = tx.Rollback(ctx) }()

	// Atomic replace: when the caller supplies a set of rule IDs to evict,
	// drop them BEFORE the new inserts run, all inside the same tx so a
	// later collision rolls everything back. Used by the wizard's edit-
	// existing-group and convert-single-to-multi paths.
	if len(in.ReplaceExistingIDs) > 0 {
		if _, err := tx.Exec(ctx,
			`DELETE FROM notification_rules WHERE id = ANY($1)`,
			in.ReplaceExistingIDs,
		); err != nil {
			return apitypes.NotificationRuleGroupResponse{}, fmt.Errorf("replace delete: %w", err)
		}
	}

	// Build a unique per-row name for each condition. Naively suffixing with
	// condition_type collides when the same type appears twice in the batch
	// (e.g. three metric_threshold legs); detect duplicates up front and
	// append a 1-based index to the colliding type tokens.
	typeCount := map[string]int{}
	for _, c := range in.Conditions {
		typeCount[c.ConditionType]++
	}
	typeIdx := map[string]int{}
	rowNames := make([]string, len(in.Conditions))
	for i, c := range in.Conditions {
		if len(in.Conditions) == 1 {
			rowNames[i] = in.Name
			continue
		}
		typeIdx[c.ConditionType]++
		suffix := c.ConditionType
		if typeCount[c.ConditionType] > 1 {
			suffix = fmt.Sprintf("%s %d", suffix, typeIdx[c.ConditionType])
		}
		rowNames[i] = fmt.Sprintf("%s — %s", in.Name, suffix)
	}

	out := make([]apitypes.NotificationRule, 0, len(in.Conditions))
	for i, c := range in.Conditions {
		if c.ConditionType == "" {
			return apitypes.NotificationRuleGroupResponse{}, fmt.Errorf("condition %d: condition_type required", i)
		}
		if c.ConditionType == apitypes.ConditionAuditAction {
			if err := validateAuditActionParams(c.ConditionParams); err != nil {
				return apitypes.NotificationRuleGroupResponse{}, fmt.Errorf("condition %d: %w", i, err)
			}
		}
		rowName := rowNames[i]
		paramsJSON, err := json.Marshal(orEmptyAny(c.ConditionParams))
		if err != nil {
			return apitypes.NotificationRuleGroupResponse{}, fmt.Errorf("rule %d: marshal params: %w", i, err)
		}

		row := tx.QueryRow(ctx, `
			INSERT INTO notification_rules
				(name, enabled, condition_type, condition_params,
				 channel_ids, severity, throttle_sec,
				 repeat_interval_sec, notify_on_resolve,
				 target_host_ids, target_tags, target_group_ids,
				 group_id, created_by)
			VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13,$14)
			RETURNING id, name, enabled, condition_type, condition_params,
			          channel_ids, severity, throttle_sec,
			          repeat_interval_sec, notify_on_resolve,
			          target_host_ids, target_tags, target_group_ids,
			          group_id,
			          created_at, COALESCE(created_by,'')`,
			rowName, in.Enabled, c.ConditionType, paramsJSON,
			channels, sev, in.ThrottleSec,
			in.RepeatIntervalSec, in.NotifyOnResolve,
			hostIDs, tags, tgtGroups,
			groupID, nullableString(createdBy),
		)
		r, err := scanRule(row.Scan)
		if err != nil {
			if pgIsUniqueViolation(err) {
				return apitypes.NotificationRuleGroupResponse{}, fmt.Errorf("rule %d: name %q already exists", i, rowName)
			}
			return apitypes.NotificationRuleGroupResponse{}, fmt.Errorf("rule %d: %w", i, err)
		}
		out = append(out, r)
	}
	if err := tx.Commit(ctx); err != nil {
		return apitypes.NotificationRuleGroupResponse{}, err
	}
	return apitypes.NotificationRuleGroupResponse{GroupID: groupID, Rules: out}, nil
}
