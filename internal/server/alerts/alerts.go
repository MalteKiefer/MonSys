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
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"sync"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/MalteKiefer/MonSys/internal/server/liveness"
	"github.com/MalteKiefer/MonSys/internal/server/notify"
	"github.com/MalteKiefer/MonSys/internal/server/probe"
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
	tags, groupIDs := e.fetchHostScope(ctx, tr.HostID)
	for _, r := range rules {
		if !r.matchesHost(tr.HostID, tags, groupIDs) {
			continue
		}
		// Only fire on transitions to states the rule cares about. Default
		// reaction is on "offline"; rule can override via params.match_states.
		states := stringSliceParam(r.ConditionParams, "match_states", []string{"offline"})
		dedup := fmt.Sprintf("host_offline:%s", tr.HostID)
		if !contains(states, tr.To) {
			// Transition leaves the alerting set. If we have an open
			// alert_state row for this dedup_key, send the all-clear
			// through the same channels and stamp resolved_at.
			body := fmt.Sprintf("Host is back online (was: %s)", tr.From)
			e.resolve(ctx, dedup, body)
			continue
		}
		subj := fmt.Sprintf("[mon] %s is %s", tr.Hostname, tr.To)
		body := fmt.Sprintf("Host %s transitioned %s → %s at %s",
			tr.Hostname, tr.From, tr.To, tr.At.UTC().Format(time.RFC3339))
		e.fire(ctx, r, subj, body, dedup)
	}
}

func (e *Engine) handleMonitor(ctx context.Context, ev probe.ResultEvent) {
	if ev.Result.Status == probe.StatusOK {
		// Monitor recovered. Resolve any open alert_state rows tied to
		// this monitor's dedup keys (monitor_failed + cert_expiring) so
		// operators get the matching all-clear.
		body := fmt.Sprintf("Monitor %q (%s) recovered: status ok after %d ms",
			ev.Name, ev.Type, ev.Result.LatencyMS)
		e.resolve(ctx, fmt.Sprintf("monitor_failed:%s", ev.MonitorID), body)
		if ev.Type == "cert" {
			e.resolve(ctx, fmt.Sprintf("cert_expiring:%s", ev.MonitorID), body)
		}
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
	e.runRepeatReminders(ctx)
}

// runRepeatReminders re-dispatches an open alert through its rule's channels
// when repeat_interval_sec has elapsed since the last fire. The condition
// itself is not re-evaluated here; we trust that resolve() (driven by the
// transition handlers) clears alert_state the moment the underlying state
// recovers, so any row still open with resolved_at IS NULL means the alert
// is genuinely still active.
func (e *Engine) runRepeatReminders(ctx context.Context) {
	rows, err := e.Pool.Query(ctx, `
		SELECT s.dedup_key, s.subject, s.channel_ids, s.severity,
		       r.repeat_interval_sec, r.name,
		       EXTRACT(EPOCH FROM (now() - s.last_fired_at))::INT AS elapsed_sec,
		       EXTRACT(EPOCH FROM (now() - s.opened_at))::INT     AS open_sec
		FROM alert_state s
		JOIN notification_rules r ON r.id = s.rule_id
		WHERE s.resolved_at IS NULL
		  AND r.enabled = TRUE
		  AND r.repeat_interval_sec >= 60
		  AND now() - s.last_fired_at >= make_interval(secs => r.repeat_interval_sec)`)
	if err != nil {
		slog.Warn("alerts: repeat-reminder query", "err", err)
		return
	}
	defer rows.Close()

	type reminder struct {
		dedup, subject, severity, ruleName string
		channels                           []uuid.UUID
		repeatSec, elapsedSec, openSec     int
	}
	var pending []reminder
	for rows.Next() {
		var rm reminder
		if err := rows.Scan(&rm.dedup, &rm.subject, &rm.channels, &rm.severity,
			&rm.repeatSec, &rm.ruleName, &rm.elapsedSec, &rm.openSec); err != nil {
			slog.Warn("alerts: repeat-reminder scan", "err", err)
			continue
		}
		pending = append(pending, rm)
	}
	if err := rows.Err(); err != nil {
		slog.Warn("alerts: repeat-reminder iter", "err", err)
	}

	if len(pending) == 0 {
		return
	}
	if e.inQuietHours(ctx, time.Now()) {
		// Honour the quiet window the same way fire() does. Don't bump
		// last_fired_at so the next non-quiet tick sends the reminder.
		return
	}

	for _, rm := range pending {
		body := fmt.Sprintf("Reminder: incident still active.\nOpen for %s; previous notification %s ago.",
			fmtDurationSecs(rm.openSec), fmtDurationSecs(rm.elapsedSec))
		m := notify.Message{
			Subject:  "[Reminder] " + rm.subject,
			Body:     body,
			Severity: rm.severity,
		}
		delivered := []string{}
		errMap := map[string]string{}
		for _, chID := range rm.channels {
			if err := e.sendChannel(ctx, chID, m); err != nil {
				errMap[chID.String()] = err.Error()
				continue
			}
			delivered = append(delivered, chID.String())
		}

		errJSON, _ := json.Marshal(errMap)
		if _, err := e.Pool.Exec(ctx, `
			INSERT INTO alert_history (rule_id, rule_name, severity, subject, body,
			                           dedup_key, delivered_to, delivery_errors)
			SELECT s.rule_id, $2, s.severity, $3, $4, $1, $5, $6
			FROM alert_state s WHERE s.dedup_key = $1`,
			rm.dedup, rm.ruleName, m.Subject, body, delivered, errJSON); err != nil {
			slog.Warn("alerts: repeat-reminder history insert", "dedup", rm.dedup, "err", err)
		}

		// Bump last_fired_at only when at least one channel delivered, so a
		// total dispatch failure causes the next tick to retry the reminder
		// instead of silently waiting another full repeat_interval.
		if len(delivered) > 0 {
			if _, err := e.Pool.Exec(ctx,
				`UPDATE alert_state SET last_fired_at = now()
				   WHERE dedup_key = $1 AND resolved_at IS NULL`, rm.dedup); err != nil {
				slog.Warn("alerts: repeat-reminder last_fired update", "err", err)
			}
		}
	}
}

func fmtDurationSecs(secs int) string {
	if secs < 60 {
		return fmt.Sprintf("%ds", secs)
	}
	if secs < 3600 {
		return fmt.Sprintf("%dm%02ds", secs/60, secs%60)
	}
	return fmt.Sprintf("%dh%02dm", secs/3600, (secs%3600)/60)
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
		tags, groupIDs := e.fetchHostScope(ctx, id)
		if !r.matchesHost(id, tags, groupIDs) {
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
		tags, groupIDs := e.fetchHostScope(ctx, id)
		if !r.matchesHost(id, tags, groupIDs) {
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

	// Quiet-hour gate. We still write an alert_history row so the audit trail
	// stays intact, but skip dispatching to channels and mark the suppression
	// in delivery_errors. recentlyFired() above only counts rows that actually
	// delivered, so a suppressed alert won't silence the next one.
	if e.inQuietHours(ctx, time.Now()) {
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
	_, err = e.Pool.Exec(ctx, `
		INSERT INTO alert_history (rule_id, rule_name, severity, subject, body,
		                           dedup_key, delivered_to, delivery_errors)
		VALUES ($1,$2,$3,$4,$5,$6,$7,$8)`,
		r.ID, r.Name, severity, subject, body, dedup, delivered, errJSON)
	if err != nil {
		slog.Warn("alerts: alert_history insert", "err", err)
	}

	// Track this as currently-open. The PK is dedup_key, so a re-fire of the
	// same condition updates last_fired_at + refreshes channel_ids; if the
	// previous incarnation had been resolved we treat this as a brand-new
	// open and reset opened_at.
	hostArg, monitorArg := splitDedupKey(dedup)
	channelUUIDs := parseChannelUUIDs(r.ChannelIDs)
	if _, err := e.Pool.Exec(ctx, `
		INSERT INTO alert_state (dedup_key, rule_id, host_id, monitor_id,
		                         severity, subject, channel_ids,
		                         opened_at, last_fired_at, resolved_at)
		VALUES ($1,$2,$3,$4,$5,$6,$7, now(), now(), NULL)
		ON CONFLICT (dedup_key) DO UPDATE SET
		    rule_id       = EXCLUDED.rule_id,
		    host_id       = EXCLUDED.host_id,
		    monitor_id    = EXCLUDED.monitor_id,
		    severity      = EXCLUDED.severity,
		    subject       = EXCLUDED.subject,
		    channel_ids   = EXCLUDED.channel_ids,
		    last_fired_at = now(),
		    opened_at     = CASE WHEN alert_state.resolved_at IS NOT NULL
		                         THEN now() ELSE alert_state.opened_at END,
		    resolved_at   = NULL`,
		dedup, r.ID, hostArg, monitorArg, severity, subject, channelUUIDs); err != nil {
		// Escalated to Error: a missing alert_state row breaks the future
		// resolved path — resolve() looks up by dedup_key and silently
		// no-ops when there's no open row, so operators would never see
		// the all-clear when this incident eventually clears.
		slog.Error("alerts: alert_state upsert", "dedup", dedup, "err", err)
	}
}

// resolve looks up the open alert_state row for dedup, dispatches a resolved
// Message to the channels recorded on that row, and stamps resolved_at. No-op
// if there is no open row (i.e. nothing to clear). Resolved messages are
// always severity "info" regardless of the original severity.
//
// Two correctness rules:
//
//  1. We only stamp resolved_at when at least one channel actually delivered
//     the all-clear. If every dispatch errored (e.g. a transient SMTP outage
//     during recovery) the open row stays put so the next liveness/monitor
//     tick — or the next periodic resolve attempt — can retry. Otherwise a
//     single SMTP flap during recovery would silently drop the resolved mail
//     forever and operators would never learn the incident closed.
//
//  2. Quiet hours apply symmetrically with fire(): inside the silence window
//     we still clear the open record (so it doesn't linger across the rest
//     of the night) but skip the dispatch. The all-clear is intentionally
//     dropped for the same reason a fire is suppressed — the operator asked
//     for silence.
func (e *Engine) resolve(ctx context.Context, dedup, body string) {
	var (
		subject         string
		channelIDs      []uuid.UUID
		notifyOnResolve bool
	)
	// LEFT JOIN so an orphaned alert_state (rule deleted while firing) still
	// resolves cleanly; COALESCE gives those orphans the historical "send the
	// all-clear" behaviour.
	err := e.Pool.QueryRow(ctx, `
		SELECT s.subject, s.channel_ids, COALESCE(r.notify_on_resolve, TRUE)
		FROM alert_state s
		LEFT JOIN notification_rules r ON r.id = s.rule_id
		WHERE s.dedup_key = $1 AND s.resolved_at IS NULL`, dedup,
	).Scan(&subject, &channelIDs, &notifyOnResolve)
	if err != nil {
		// No open row is the common case (most transitions don't clear
		// anything because nothing was firing). Only log other errors.
		if !errors.Is(err, pgx.ErrNoRows) {
			slog.Warn("alerts: alert_state lookup", "dedup", dedup, "err", err)
		}
		return
	}

	// Operator opted out of all-clear notifications for this rule. We still
	// stamp resolved_at so dashboards close the incident; only the channel
	// dispatch is skipped.
	if !notifyOnResolve {
		if _, err := e.Pool.Exec(ctx,
			`UPDATE alert_state SET resolved_at = now()
			   WHERE dedup_key = $1 AND resolved_at IS NULL`, dedup); err != nil {
			slog.Warn("alerts: alert_state resolve update (notify_on_resolve=false)", "err", err)
		}
		return
	}

	// Quiet-hours symmetry with fire(): clear the open record but don't
	// dispatch. We log at info so operators can correlate why no all-clear
	// arrived during a silence window.
	if e.inQuietHours(ctx, time.Now()) {
		slog.Info("alerts: resolve suppressed by quiet hours", "dedup", dedup)
		if _, err := e.Pool.Exec(ctx,
			`UPDATE alert_state SET resolved_at = now()
			   WHERE dedup_key = $1 AND resolved_at IS NULL`, dedup); err != nil {
			slog.Warn("alerts: alert_state resolve update (quiet)", "err", err)
		}
		return
	}

	m := notify.Message{
		Subject:  "[Resolved] " + subject,
		Body:     body,
		Severity: "info",
	}
	delivered := 0
	for _, chID := range channelIDs {
		if err := e.sendChannel(ctx, chID, m); err != nil {
			slog.Warn("alerts: resolved dispatch failed",
				"channel", chID, "dedup", dedup, "err", err)
			continue
		}
		delivered++
	}

	if delivered == 0 {
		// Every dispatch failed (or there were no channels). Leave the open
		// alert_state row in place so a future tick retries the all-clear.
		// Escalated to Error because a stuck open record means operators
		// will never see the resolution unless something triggers another
		// attempt.
		slog.Error("alerts: resolve dispatched zero channels; leaving alert_state open for retry",
			"dedup", dedup, "channels", len(channelIDs))
		return
	}

	if _, err := e.Pool.Exec(ctx,
		`UPDATE alert_state SET resolved_at = now()
		   WHERE dedup_key = $1 AND resolved_at IS NULL`, dedup); err != nil {
		slog.Warn("alerts: alert_state resolve update", "err", err)
	}
}

// splitDedupKey extracts the host_id / monitor_id from a dedup_key formed as
// "<kind>:<uuid>" and returns them as driver-friendly any values. nil means
// "store NULL"; this lets the upsert skip host_id for monitor-scoped rules
// and vice versa without a separate code path.
func splitDedupKey(dedup string) (hostArg, monitorArg any) {
	idx := strings.IndexByte(dedup, ':')
	if idx < 0 || idx == len(dedup)-1 {
		return nil, nil
	}
	kind, raw := dedup[:idx], dedup[idx+1:]
	id, err := uuid.Parse(raw)
	if err != nil {
		return nil, nil
	}
	switch kind {
	case "host_offline", "login_failed", "security_updates":
		return id, nil
	case "monitor_failed", "cert_expiring":
		return nil, id
	}
	return nil, nil
}

func parseChannelUUIDs(in []string) []uuid.UUID {
	out := make([]uuid.UUID, 0, len(in))
	for _, s := range in {
		if u, err := uuid.Parse(s); err == nil {
			out = append(out, u)
		}
	}
	return out
}

// --- quiet hours -----------------------------------------------------------

// inQuietHours mirrors the algorithm in internal/agent/agent.go:inQuietHours
// (wraparound windows like 22:00→06:00 supported, optional day filter), but
// resolves the comparison time in the configured timezone. The cache layer
// reads notification_settings at most once per cacheTTL to avoid hammering
// the DB during a sustained outage spawning lots of alerts.
const quietCacheTTL = 60 * time.Second

func (e *Engine) inQuietHours(ctx context.Context, now time.Time) bool {
	q := e.loadQuietConfig(ctx)
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
// transient outage doesn't accidentally suppress alerts. The ctx is the
// engine's run-loop ctx so a graceful shutdown actually cancels the lookup
// instead of stranding the goroutine on a pool wait.
func (e *Engine) loadQuietConfig(ctx context.Context) *quietConfig {
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
	err := e.Pool.QueryRow(ctx, `
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

// ErrSmtpNotConfigured is the alerts-side mirror of store.ErrSmtpNotConfigured.
// We don't import it directly to avoid pulling the store package into the
// alerts dispatch path (the loadSmtpDispatchConfig comment below explains the
// intentional duplication). errors.Is across packages doesn't matter here —
// what matters is that channel last_error and the delivery_errors map surface
// a human-readable message instead of a wrapped pgx.ErrNoRows.
var ErrSmtpNotConfigured = errors.New("smtp transport is not configured — set it under /admin/mail")

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
		// Translate the "no row yet" case into a clear, operator-facing
		// sentinel. Everything else stays wrapped so transient DB errors
		// still surface their underlying cause. Don't wrap ErrSmtpNotConfigured
		// itself — operators would just see "smtp settings: smtp transport
		// is not configured…" twice.
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, ErrSmtpNotConfigured
		}
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
	//
	// The 24h upper bound is a Timescale chunk-pruning hint: alert_history is
	// a hypertable on `at` with 365-day retention, and without an absolute
	// time bound the planner has to scan every chunk on every fire. 24h is a
	// comfortable multiple of the largest reasonable throttle_sec — operators
	// don't care about a row from 6 months ago. The throttle bound (`within`)
	// stays the actual filter; the 24h bound exists purely so the planner
	// can drop old chunks. make_interval(secs => 86400) avoids the pgx int||
	// text gotcha we hit elsewhere.
	var n int
	err := e.Pool.QueryRow(ctx, `
		SELECT count(*) FROM alert_history
		WHERE dedup_key = $1
		  AND at >= now() - make_interval(secs => 86400)
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
	ID                uuid.UUID
	Name              string
	ConditionParams   map[string]any
	ChannelIDs        []string
	Severity          string
	ThrottleSec       int
	RepeatIntervalSec int
	NotifyOnResolve   bool
	TargetHostIDs     []uuid.UUID
	TargetTags        []string
	TargetGroupIDs    []uuid.UUID
}

// matchesHost decides whether a rule applies to a specific host. All-empty
// target sets behave as a wildcard (the historical "applies everywhere"
// behaviour). When any of the three target slices is non-empty the rule
// matches only when the host is referenced by at least one of them.
func (r ruleRow) matchesHost(hostID uuid.UUID, tags []string, groupIDs []uuid.UUID) bool {
	if len(r.TargetHostIDs) == 0 && len(r.TargetTags) == 0 && len(r.TargetGroupIDs) == 0 {
		return true
	}
	for _, id := range r.TargetHostIDs {
		if id == hostID {
			return true
		}
	}
	for _, want := range r.TargetTags {
		for _, have := range tags {
			if want == have {
				return true
			}
		}
	}
	for _, want := range r.TargetGroupIDs {
		for _, have := range groupIDs {
			if want == have {
				return true
			}
		}
	}
	return false
}

func (e *Engine) loadRules(ctx context.Context, conditionType string) ([]ruleRow, error) {
	rows, err := e.Pool.Query(ctx, `
		SELECT id, name, condition_params, channel_ids, severity, throttle_sec,
		       repeat_interval_sec, notify_on_resolve,
		       target_host_ids, target_tags, target_group_ids
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
		if err := rows.Scan(&r.ID, &r.Name, &paramsRaw, &channelUUIDs, &r.Severity, &r.ThrottleSec,
			&r.RepeatIntervalSec, &r.NotifyOnResolve,
			&r.TargetHostIDs, &r.TargetTags, &r.TargetGroupIDs); err != nil {
			return nil, err
		}
		r.ConditionParams = map[string]any{}
		if len(paramsRaw) > 0 {
			if err := json.Unmarshal(paramsRaw, &r.ConditionParams); err != nil {
				// Don't fail the whole load — degrade to an empty params
				// map so the rule still evaluates with defaults. Logging
				// the rule_id makes the corrupt JSONB row discoverable
				// without having to dump the whole table.
				slog.Warn("alerts: rule condition_params unmarshal",
					"rule_id", r.ID, "column", "condition_params", "err", err)
				r.ConditionParams = map[string]any{}
			}
		}
		for _, u := range channelUUIDs {
			r.ChannelIDs = append(r.ChannelIDs, u.String())
		}
		out = append(out, r)
	}
	return out, rows.Err()
}

// fetchHostScope returns the tag list and host_group ids the host belongs
// to, used by ruleRow.matchesHost. Empty results on any error so an
// observability glitch never silently kills alerts; the caller treats
// "(nil, nil)" the same as "host has no scope" — only rules with empty
// targets will match.
func (e *Engine) fetchHostScope(ctx context.Context, hostID uuid.UUID) ([]string, []uuid.UUID) {
	var tags []string
	var groupIDs []uuid.UUID
	if err := e.Pool.QueryRow(ctx, `
		SELECT
		  COALESCE((SELECT array_agg(tag)         FROM host_tags          WHERE host_id = $1), '{}'),
		  COALESCE((SELECT array_agg(group_id)    FROM host_group_members WHERE host_id = $1), '{}')
	`, hostID).Scan(&tags, &groupIDs); err != nil {
		slog.Warn("alerts: fetch host scope", "host_id", hostID, "err", err)
		return nil, nil
	}
	return tags, groupIDs
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
