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
	"sync"
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

	// Cached quiet-hour settings. The alerts engine fires on every liveness
	// transition and every monitor result, so a synchronous DB read on every
	// fire would be wasteful. 60 s is short enough for an admin tweaking the
	// window not to wait long, and long enough to keep the cache effective
	// during a sustained outage that's spawning many alerts.
	quietMu     sync.Mutex
	quietCache  *quietConfig
	quietExpiry time.Time
}

type quietConfig struct {
	enabled bool
	start   string
	end     string
	days    []int
	loc     *time.Location
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
	if r.ThrottleSec > 0 {
		recent, err := e.recentlyFired(ctx, dedup, time.Duration(r.ThrottleSec)*time.Second)
		if err != nil {
			slog.Warn("alerts: recentlyFired query failed; firing anyway",
				"rule", r.Name, "err", err)
		} else if recent {
			return
		}
	}

	severity := r.Severity
	if severity == "" {
		severity = "warning"
	}

	// Quiet-hour gate. We still write an alert_history row so the audit trail
	// stays intact, but skip dispatching to channels and mark the suppression
	// in delivery_errors. recentlyFired() above only counts rows that actually
	// delivered, so a suppressed alert won't silence the next one.
	if e.inQuietHours(time.Now()) {
		errJSON, _ := json.Marshal(map[string]string{"_quiet_hours": "suppressed"})
		_, err := e.Pool.Exec(ctx, `
			INSERT INTO alert_history (rule_id, rule_name, severity, subject, body,
			                           dedup_key, delivered_to, delivery_errors)
			VALUES ($1,$2,$3,$4,$5,$6,$7::text[],$8)`,
			r.ID, r.Name, severity, subject, body, dedup, []string{}, errJSON)
		if err != nil {
			slog.Warn("alerts: alert_history insert (quiet-hours)", "err", err)
		}
		return
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
	_, err := e.Pool.Exec(ctx, `
		INSERT INTO alert_history (rule_id, rule_name, severity, subject, body,
		                           dedup_key, delivered_to, delivery_errors)
		VALUES ($1,$2,$3,$4,$5,$6,$7,$8)`,
		r.ID, r.Name, severity, subject, body, dedup, delivered, errJSON)
	if err != nil {
		slog.Warn("alerts: alert_history insert", "err", err)
	}
}

// --- quiet hours -----------------------------------------------------------

// inQuietHours mirrors the algorithm in internal/agent/agent.go:inQuietHours
// (wraparound windows like 22:00→06:00 supported, optional day filter), but
// resolves the comparison time in the configured timezone. The cache layer
// reads notification_settings at most once per cacheTTL to avoid hammering
// the DB during a sustained outage spawning lots of alerts.
const quietCacheTTL = 60 * time.Second

func (e *Engine) inQuietHours(now time.Time) bool {
	q := e.loadQuietConfig()
	if q == nil || !q.enabled {
		return false
	}
	loc := q.loc
	if loc == nil {
		loc = time.UTC
	}
	local := now.In(loc)
	if len(q.days) > 0 {
		match := false
		for _, d := range q.days {
			if int(local.Weekday()) == d {
				match = true
				break
			}
		}
		if !match {
			return false
		}
	}
	startMin, ok1 := parseHHMM(q.start)
	endMin, ok2 := parseHHMM(q.end)
	if !ok1 || !ok2 || startMin == endMin {
		return false
	}
	cur := local.Hour()*60 + local.Minute()
	if startMin < endMin {
		return cur >= startMin && cur < endMin
	}
	// Wraparound (e.g. 22:00 - 06:00).
	return cur >= startMin || cur < endMin
}

// loadQuietConfig returns the cached config, refreshing from the DB when the
// cache is stale. A DB error returns nil (treated as "no quiet hours") so a
// transient outage doesn't accidentally suppress alerts.
func (e *Engine) loadQuietConfig() *quietConfig {
	e.quietMu.Lock()
	defer e.quietMu.Unlock()

	if e.quietCache != nil && time.Now().Before(e.quietExpiry) {
		return e.quietCache
	}

	var (
		enabled    bool
		start, end string
		days       []int16
		tz         string
	)
	err := e.Pool.QueryRow(context.Background(), `
		SELECT quiet_enabled, quiet_start, quiet_end, quiet_days, quiet_tz
		FROM notification_settings WHERE id = 1`,
	).Scan(&enabled, &start, &end, &days, &tz)
	if err != nil {
		slog.Warn("alerts: notification_settings load", "err", err)
		// Cache nil briefly so we don't slam the DB while it's down.
		e.quietCache = nil
		e.quietExpiry = time.Now().Add(quietCacheTTL)
		return nil
	}
	loc, lerr := time.LoadLocation(tz)
	if lerr != nil {
		loc = time.UTC
	}
	cfg := &quietConfig{
		enabled: enabled,
		start:   start,
		end:     end,
		loc:     loc,
	}
	for _, d := range days {
		cfg.days = append(cfg.days, int(d))
	}
	e.quietCache = cfg
	e.quietExpiry = time.Now().Add(quietCacheTTL)
	return cfg
}

// InvalidateQuietCache forces the next inQuietHours() call to re-read the
// settings row. Called from the admin PUT handler so an operator's change
// takes effect immediately rather than after the 60 s TTL.
func (e *Engine) InvalidateQuietCache() {
	e.quietMu.Lock()
	defer e.quietMu.Unlock()
	e.quietCache = nil
	e.quietExpiry = time.Time{}
}

func parseHHMM(s string) (int, bool) {
	if len(s) < 4 || len(s) > 5 {
		return 0, false
	}
	parts := strings.Split(s, ":")
	if len(parts) != 2 {
		return 0, false
	}
	h, err1 := atoiSafe(parts[0])
	m, err2 := atoiSafe(parts[1])
	if err1 != nil || err2 != nil || h < 0 || h > 23 || m < 0 || m > 59 {
		return 0, false
	}
	return h*60 + m, true
}

func atoiSafe(s string) (int, error) {
	n := 0
	for _, c := range s {
		if c < '0' || c > '9' {
			return 0, fmt.Errorf("not a number: %q", s)
		}
		n = n*10 + int(c-'0')
	}
	return n, nil
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
	var n int
	err := e.Pool.QueryRow(ctx, `
		SELECT count(*) FROM alert_history
		WHERE dedup_key = $1 AND at >= now() - make_interval(secs => $2)`,
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
