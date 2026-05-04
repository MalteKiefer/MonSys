// Package alerts is the rule-evaluation engine.
//
// Subscribes to inputs (liveness transitions, monitor results) and to
// periodic ticks (for stateful queries like "any failed logins in the last
// 5 min above threshold N"), evaluates each rule, and dispatches matching
// alerts through the notify package.
//
// Throttling uses a dedup_key per rule+entity (e.g. host id, monitor id) so
// we don't spam during a sustained outage.
package alerts

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/pr0ph37/mon/internal/server/liveness"
	"github.com/pr0ph37/mon/internal/server/notify"
	"github.com/pr0ph37/mon/internal/server/probe"
)

// Engine ties inputs to rule evaluation. It is constructed once at server
// start; Run(ctx) is the single goroutine that drives everything.
type Engine struct {
	Pool *pgxpool.Pool

	LivenessOut <-chan liveness.Transition
	MonitorOut  <-chan probe.ResultEvent

	// PeriodicInterval determines how often we run "stateful" checks like
	// "is there a host whose security_updates pending count > N?".
	PeriodicInterval time.Duration
}

func New(pool *pgxpool.Pool, livenessOut <-chan liveness.Transition, monitorOut <-chan probe.ResultEvent) *Engine {
	return &Engine{
		Pool:             pool,
		LivenessOut:      livenessOut,
		MonitorOut:       monitorOut,
		PeriodicInterval: 60 * time.Second,
	}
}

func (e *Engine) Run(ctx context.Context) {
	periodic := time.NewTicker(e.PeriodicInterval)
	defer periodic.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case tr := <-e.LivenessOut:
			e.handleLiveness(ctx, tr)
		case ev := <-e.MonitorOut:
			e.handleMonitor(ctx, ev)
		case <-periodic.C:
			e.runPeriodic(ctx)
		}
	}
}

// --- event handlers --------------------------------------------------------

func (e *Engine) handleLiveness(ctx context.Context, tr liveness.Transition) {
	rules, err := e.loadRules(ctx, "host_offline")
	if err != nil {
		slog.Warn("alerts: load host_offline rules", "err", err)
		return
	}
	for _, r := range rules {
		// Only fire on transitions to states the rule cares about. Default
		// reaction is on "offline"; rule can override via params.match_states.
		states := stringSliceParam(r.ConditionParams, "match_states", []string{"offline"})
		if !contains(states, tr.To) {
			continue
		}
		dedup := fmt.Sprintf("host_offline:%s", tr.HostID)
		subj := fmt.Sprintf("[mon] %s is %s", tr.Hostname, tr.To)
		body := fmt.Sprintf("Host %s transitioned %s → %s at %s",
			tr.Hostname, tr.From, tr.To, tr.At.UTC().Format(time.RFC3339))
		e.fire(ctx, r, subj, body, dedup)
	}
}

func (e *Engine) handleMonitor(ctx context.Context, ev probe.ResultEvent) {
	if ev.Result.Status == probe.StatusOK {
		return
	}

	// monitor_failed: any non-OK result.
	if ev.Result.Status == probe.StatusFail {
		rules, err := e.loadRules(ctx, "monitor_failed")
		if err == nil {
			for _, r := range rules {
				if !ruleMatchesMonitor(r, ev) {
					continue
				}
				dedup := fmt.Sprintf("monitor_failed:%s", ev.MonitorID)
				subj := fmt.Sprintf("[mon] monitor %s/%s failed", ev.Type, ev.Name)
				body := fmt.Sprintf("Monitor %q (%s) reported %s after %d ms\n%s",
					ev.Name, ev.Type, ev.Result.Status, ev.Result.LatencyMS, ev.Result.Detail)
				e.fire(ctx, r, subj, body, dedup)
			}
		} else {
			slog.Warn("alerts: load monitor_failed rules", "err", err)
		}
	}

	// cert_expiring: cert monitors with status warn/fail surface here too.
	if ev.Type == "cert" && (ev.Result.Status == probe.StatusWarn || ev.Result.Status == probe.StatusFail) {
		rules, err := e.loadRules(ctx, "cert_expiring")
		if err != nil {
			slog.Warn("alerts: load cert_expiring rules", "err", err)
			return
		}
		for _, r := range rules {
			dedup := fmt.Sprintf("cert_expiring:%s", ev.MonitorID)
			subj := fmt.Sprintf("[mon] cert %s expiring soon", ev.Name)
			body := fmt.Sprintf("Cert monitor %q reported %s\n%s",
				ev.Name, ev.Result.Status, ev.Result.Detail)
			// cert_expiring rules inherit the cert monitor's severity
			// (warn → warning, fail → critical) unless explicitly set.
			rr := r
			if rr.Severity == "" || rr.Severity == "info" {
				if ev.Result.Status == probe.StatusFail {
					rr.Severity = "critical"
				} else {
					rr.Severity = "warning"
				}
			}
			e.fire(ctx, rr, subj, body, dedup)
		}
	}
}

// runPeriodic evaluates rules whose conditions are stateful queries.
func (e *Engine) runPeriodic(ctx context.Context) {
	if rules, err := e.loadRules(ctx, "login_failed_threshold"); err == nil {
		for _, r := range rules {
			e.evalLoginFailed(ctx, r)
		}
	}
	if rules, err := e.loadRules(ctx, "security_updates_pending"); err == nil {
		for _, r := range rules {
			e.evalSecurityUpdates(ctx, r)
		}
	}
}

// evalLoginFailed counts failed login_events in the last `window_sec` per host
// and fires when the count > `threshold`.
func (e *Engine) evalLoginFailed(ctx context.Context, r ruleRow) {
	threshold := intParam(r.ConditionParams, "threshold", 10)
	windowSec := intParam(r.ConditionParams, "window_sec", 300)

	rows, err := e.Pool.Query(ctx, `
		SELECT h.id, h.hostname, count(*) AS failed
		FROM login_events le JOIN hosts h ON h.id = le.host_id
		WHERE le.success = FALSE
		  AND le.time >= now() - make_interval(secs => $1)
		GROUP BY h.id, h.hostname
		HAVING count(*) > $2`,
		windowSec, threshold)
	if err != nil {
		slog.Warn("alerts: login_failed query", "err", err)
		return
	}
	defer rows.Close()
	for rows.Next() {
		var id uuid.UUID
		var hostname string
		var n int
		if err := rows.Scan(&id, &hostname, &n); err != nil {
			continue
		}
		dedup := fmt.Sprintf("login_failed:%s", id)
		subj := fmt.Sprintf("[mon] %s: %d failed logins in %ds", hostname, n, windowSec)
		body := fmt.Sprintf("Host %s observed %d failed login attempts within the last %d seconds (threshold %d).",
			hostname, n, windowSec, threshold)
		e.fire(ctx, r, subj, body, dedup)
	}
}

// evalSecurityUpdates fires when latest packages summary reports
// security_updates >= threshold (default 1 — any pending security update).
func (e *Engine) evalSecurityUpdates(ctx context.Context, r ruleRow) {
	threshold := intParam(r.ConditionParams, "threshold", 1)

	// Latest summary per host.
	rows, err := e.Pool.Query(ctx, `
		SELECT DISTINCT ON (host_id) host_id, security_updates, time
		FROM metrics_packages_summary
		ORDER BY host_id, time DESC`)
	if err != nil {
		slog.Warn("alerts: security_updates query", "err", err)
		return
	}
	defer rows.Close()
	for rows.Next() {
		var id uuid.UUID
		var sec int
		var t time.Time
		if err := rows.Scan(&id, &sec, &t); err != nil {
			continue
		}
		if sec < threshold {
			continue
		}
		dedup := fmt.Sprintf("security_updates:%s", id)
		subj := fmt.Sprintf("[mon] host %s: %d pending security updates", id, sec)
		body := fmt.Sprintf("Host %s has %d security updates available (threshold %d, observed at %s).",
			id, sec, threshold, t.UTC().Format(time.RFC3339))
		e.fire(ctx, r, subj, body, dedup)
	}
}

// --- fire path -------------------------------------------------------------

func (e *Engine) fire(ctx context.Context, r ruleRow, subject, body, dedup string) {
	// throttle_sec == 0 historically meant "no throttling", which made the
	// engine spam every tick during sustained outages. Treat 0 as "use the
	// default" (300s) instead; migration 0015 also rewrites stored zeros.
	throttle := r.ThrottleSec
	if throttle <= 0 {
		throttle = 300
	}
	recent, err := e.recentlyFired(ctx, dedup, time.Duration(throttle)*time.Second)
	if err != nil {
		slog.Warn("alerts: recentlyFired query failed; firing anyway",
			"rule", r.Name, "err", err)
	} else if recent {
		return
	}

	severity := r.Severity
	if severity == "" {
		severity = "warning"
	}
	m := notify.Message{Subject: subject, Body: body, Severity: severity}

	delivered := []string{}
	errors := map[string]string{}
	for _, ch := range r.ChannelIDs {
		chID, err := uuid.Parse(ch)
		if err != nil {
			errors[ch] = "invalid channel id"
			continue
		}
		if err := e.sendChannel(ctx, chID, m); err != nil {
			errors[chID.String()] = err.Error()
			continue
		}
		delivered = append(delivered, chID.String())
	}

	errJSON, _ := json.Marshal(errors)
	_, err = e.Pool.Exec(ctx, `
		INSERT INTO alert_history (rule_id, rule_name, severity, subject, body,
		                           dedup_key, delivered_to, delivery_errors)
		VALUES ($1,$2,$3,$4,$5,$6,$7,$8)`,
		r.ID, r.Name, severity, subject, body, dedup, delivered, errJSON)
	if err != nil {
		slog.Warn("alerts: alert_history insert", "err", err)
	}
}

// sendChannel reads the channel + dispatches. Done here (not via store) to
// avoid an alerts→store→alerts import cycle. For email channels the global
// SMTP transport from smtp_settings is merged in just before dispatch, so a
// recipient_email row plus admin-saved SMTP credentials is enough to send.
func (e *Engine) sendChannel(ctx context.Context, id uuid.UUID, m notify.Message) error {
	var (
		typ, name string
		enabled   bool
		raw       []byte
		recipient string
	)
	err := e.Pool.QueryRow(ctx,
		`SELECT type, name, enabled, config, COALESCE(recipient_email, '')
		   FROM notification_channels WHERE id = $1`, id,
	).Scan(&typ, &name, &enabled, &raw, &recipient)
	if err != nil {
		return err
	}
	if !enabled {
		return fmt.Errorf("channel %s disabled", name)
	}
	cfg := map[string]any{}
	if len(raw) > 0 {
		_ = json.Unmarshal(raw, &cfg)
	}
	if typ == "email" || typ == "smtp" {
		if recipient == "" {
			return fmt.Errorf("channel %s has no recipient_email", name)
		}
		merged, err := e.loadSmtpDispatchConfig(ctx, recipient)
		if err != nil {
			return err
		}
		cfg = merged
	}
	sendErr := notify.Dispatch(ctx, notify.Channel{
		ID: id.String(), Type: typ, Name: name, Config: cfg,
	}, m)
	if sendErr != nil {
		_, _ = e.Pool.Exec(ctx,
			`UPDATE notification_channels SET last_error = $2 WHERE id = $1`,
			id, truncate(sendErr.Error(), 500))
	} else {
		_, _ = e.Pool.Exec(ctx,
			`UPDATE notification_channels SET last_used_at = now(), last_error = NULL WHERE id = $1`,
			id)
	}
	return sendErr
}

// loadSmtpDispatchConfig reads the global smtp_settings singleton and returns
// the runtime config map notify.SMTP expects. Mirrored from the store helper
// (kept in sync — small enough to duplicate vs. introducing an import cycle).
func (e *Engine) loadSmtpDispatchConfig(ctx context.Context, recipient string) (map[string]any, error) {
	var (
		host, username, password, from string
		port                           int
		starttls, tls, insecure        bool
	)
	err := e.Pool.QueryRow(ctx, `
		SELECT host, port, username, password, from_address,
		       starttls, tls, insecure_skip_verify
		FROM smtp_settings WHERE id = 1`,
	).Scan(&host, &port, &username, &password, &from, &starttls, &tls, &insecure)
	if err != nil {
		return nil, fmt.Errorf("smtp settings: %w", err)
	}
	return map[string]any{
		"host":                 host,
		"port":                 port,
		"username":             username,
		"password":             password,
		"from":                 from,
		"to":                   []string{recipient},
		"starttls":             starttls,
		"tls":                  tls,
		"insecure_skip_verify": insecure,
	}, nil
}

func (e *Engine) recentlyFired(ctx context.Context, dedup string, within time.Duration) (bool, error) {
	// Only count rows where at least one channel actually delivered. A row
	// with an empty delivered_to means every channel errored, so the operator
	// never received the alert — suppressing the next one would compound the
	// outage (e.g. an SMTP flap silencing real alerts for the whole window).
	var n int
	err := e.Pool.QueryRow(ctx, `
		SELECT count(*) FROM alert_history
		WHERE dedup_key = $1
		  AND at >= now() - make_interval(secs => $2)
		  AND coalesce(array_length(delivered_to, 1), 0) > 0`,
		dedup, within.Seconds()).Scan(&n)
	if err != nil {
		return false, err
	}
	return n > 0, nil
}

// --- rule loader -----------------------------------------------------------

type ruleRow struct {
	ID              uuid.UUID
	Name            string
	ConditionParams map[string]any
	ChannelIDs      []string
	Severity        string
	ThrottleSec     int
}

func (e *Engine) loadRules(ctx context.Context, conditionType string) ([]ruleRow, error) {
	rows, err := e.Pool.Query(ctx, `
		SELECT id, name, condition_params, channel_ids, severity, throttle_sec
		FROM notification_rules
		WHERE enabled = TRUE AND condition_type = $1`, conditionType)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []ruleRow
	for rows.Next() {
		var r ruleRow
		var paramsRaw []byte
		var channelUUIDs []uuid.UUID
		if err := rows.Scan(&r.ID, &r.Name, &paramsRaw, &channelUUIDs, &r.Severity, &r.ThrottleSec); err != nil {
			return nil, err
		}
		r.ConditionParams = map[string]any{}
		if len(paramsRaw) > 0 {
			_ = json.Unmarshal(paramsRaw, &r.ConditionParams)
		}
		for _, u := range channelUUIDs {
			r.ChannelIDs = append(r.ChannelIDs, u.String())
		}
		out = append(out, r)
	}
	return out, rows.Err()
}

// --- helpers ---------------------------------------------------------------

func ruleMatchesMonitor(r ruleRow, ev probe.ResultEvent) bool {
	if t, ok := r.ConditionParams["monitor_type"].(string); ok && t != "" && t != ev.Type {
		return false
	}
	if n, ok := r.ConditionParams["monitor_name"].(string); ok && n != "" && n != ev.Name {
		return false
	}
	return true
}

func intParam(m map[string]any, key string, def int) int {
	switch v := m[key].(type) {
	case float64:
		return int(v)
	case int:
		return v
	}
	return def
}

func stringSliceParam(m map[string]any, key string, def []string) []string {
	switch v := m[key].(type) {
	case []any:
		out := make([]string, 0, len(v))
		for _, x := range v {
			if s, ok := x.(string); ok {
				out = append(out, s)
			}
		}
		if len(out) == 0 {
			return def
		}
		return out
	case []string:
		if len(v) == 0 {
			return def
		}
		return v
	}
	return def
}

func contains(s []string, needle string) bool {
	for _, v := range s {
		if strings.EqualFold(v, needle) {
			return true
		}
	}
	return false
}

func truncate(s string, max int) string {
	if len(s) <= max {
		return s
	}
	return s[:max]
}
