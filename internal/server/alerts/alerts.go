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

	// In-memory state cache used by state-change evaluators (container/vm/nic
	// transitions, firewall regressions, fail2ban jail disappearance, audit
	// tailing, inventory drift). Lost on restart by design — the first tick
	// after boot simply rebuilds baseline state and the next change fires.
	stateMu        sync.Mutex
	containerState map[string]string               // host:external_id -> state
	vmState        map[string]string               // host:external_id -> state
	nicSpeed       map[string]int                  // host:nic_name -> last speed_mbps
	nicMembers     map[string]int                  // host:nic_name -> last len(members)
	fail2banSeen   map[string]map[string]bool      // host -> jail -> currently present
	firewallActive map[string]bool                 // host:engine -> active
	firewallPolicy map[string]string               // host:engine -> default_input
	firewallRules  map[string]int                  // host:engine -> rule_count
	hostUptime     map[uuid.UUID]int64             // host_id -> last uptime_sec
	unexpReboot    map[uuid.UUID]time.Time         // host_id -> last fired_at (cooldown)
	auditLastAt    map[uuid.UUID]time.Time         // rule_id -> last seen audit.at
	invUsers       map[uuid.UUID]map[string]bool   // host_id -> username set
	invSudoers     map[uuid.UUID]map[string]bool   // host_id -> sudoer username set
	invDisks       map[uuid.UUID]map[string]bool   // host_id -> mountpoint set
	invNics        map[uuid.UUID]map[string]bool   // host_id -> nic name set
	invMacs        map[uuid.UUID]map[string]string // host_id -> nic name -> mac
	invKernel      map[uuid.UUID]string            // host_id -> kernel
	invDistro      map[uuid.UUID]string            // host_id -> distro

	// Bounded login-IP seen-set + per-(host,user) first-seen lookup.
	// F-6: loginNewIPSeen had no upper bound and grew unbounded with the
	// product of hosts × users × source IPs. We now cap at
	// loginNewIPSeenMax entries with FIFO eviction (oldest first).
	// F-20: replace the O(n) prefix scan over loginNewIPSeen with a
	// per-(host,user) first-seen-IP map for constant-time "have we ever
	// seen any IP for this user on this host?" lookups.
	loginNewIPSeen  map[string]bool   // host:username:source_ip -> previously seen
	loginNewIPOrder []string          // insertion order for FIFO eviction
	loginUserSeen   map[string]string // host:username -> first source_ip we ever saw

	// F-17: fetchHostScope cache. Per-tick storms (one breach producing one
	// query per host) used to do N round-trips for tags + group_id. Now
	// cached for hostScopeTTL.
	hostScopeMu    sync.Mutex
	hostScopeCache map[uuid.UUID]hostScopeEntry
}

// loginNewIPSeenMax caps the in-memory seen-set used by login_anomaly's
// new_source_ip kind. Hit values are oldest-first evicted so a sustained
// stream of new IPs doesn't pin memory indefinitely.
const loginNewIPSeenMax = 100_000

// hostScopeTTL is the lifetime of an entry in hostScopeCache. A breach storm
// firing multiple metric_threshold rules against the same host now does one
// scope round-trip per minute instead of one per rule per tick.
const hostScopeTTL = 60 * time.Second

type hostScopeEntry struct {
	tags     []string
	groupIDs []uuid.UUID
	expires  time.Time
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

		containerState:  map[string]string{},
		vmState:         map[string]string{},
		nicSpeed:        map[string]int{},
		nicMembers:      map[string]int{},
		fail2banSeen:    map[string]map[string]bool{},
		firewallActive:  map[string]bool{},
		firewallPolicy:  map[string]string{},
		firewallRules:   map[string]int{},
		hostUptime:      map[uuid.UUID]int64{},
		unexpReboot:     map[uuid.UUID]time.Time{},
		auditLastAt:     map[uuid.UUID]time.Time{},
		invUsers:        map[uuid.UUID]map[string]bool{},
		invSudoers:      map[uuid.UUID]map[string]bool{},
		invDisks:        map[uuid.UUID]map[string]bool{},
		invNics:         map[uuid.UUID]map[string]bool{},
		invMacs:         map[uuid.UUID]map[string]string{},
		invKernel:       map[uuid.UUID]string{},
		invDistro:       map[uuid.UUID]string{},
		loginNewIPSeen:  map[string]bool{},
		loginNewIPOrder: make([]string, 0, 1024),
		loginUserSeen:   map[string]string{},
		hostScopeCache:  map[uuid.UUID]hostScopeEntry{},
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
		subj := fmt.Sprintf("[MonSys] %s is %s", tr.Hostname, tr.To)
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
				subj := fmt.Sprintf("[MonSys] monitor %s/%s failed", ev.Type, ev.Name)
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
			subj := fmt.Sprintf("[MonSys] cert %s expiring soon", ev.Name)
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

	// New condition evaluators (R2). Each is internally guarded with slog.Warn
	// on DB error so a missing table or column never crashes the engine.
	e.evalMetricThreshold(ctx)
	e.evalUnexpectedReboot(ctx)
	e.evalHostFlap(ctx)
	e.evalContainerStateChange(ctx)
	e.evalVMStateChange(ctx)
	e.evalNICLinkDown(ctx)
	e.evalNICBondDegraded(ctx)
	e.evalAgentOutdated(ctx)
	e.evalImageUpdatePending(ctx)
	e.evalPackageUpdateAvailable(ctx)
	e.evalPendingReboot(ctx)
	e.evalRepoMetadataStale(ctx)
	e.evalInventoryDrift(ctx)
	e.evalLoginAnomaly(ctx)
	e.evalAuditAction(ctx)
	e.evalFirewallStateChange(ctx)
	e.evalFail2banJailDisappeared(ctx)
	e.evalCrowdSecDecisionThreshold(ctx)

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
	// F-5: clamp window to [1 s, 24 h] so a bogus param can't widen the
	// query window indefinitely.
	windowSec := clampInt(intParam(r.ConditionParams, "window_sec", 300), 1, 86400)

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
		subj := fmt.Sprintf("[MonSys] %s: %d failed logins in %ds", hostname, n, windowSec)
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
		subj := fmt.Sprintf("[MonSys] host %s: %d pending security updates", id, sec)
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
	// Most new dedup keys take the form "<kind>:<host_uuid>[:<extra>]" where
	// <extra> is a mountpoint, NIC name, workload id, etc. We only need the
	// host UUID for the alert_state row — anything after a second ':' is
	// scope/metadata and we ignore it here.
	if i := strings.IndexByte(raw, ':'); i >= 0 {
		raw = raw[:i]
	}
	id, err := uuid.Parse(raw)
	if err != nil {
		return nil, nil
	}
	switch kind {
	case "host_offline", "login_failed", "security_updates",
		"metric_threshold", "unexpected_reboot", "host_flap",
		"container_state", "vm_state", "nic_link_down", "nic_bond_degraded",
		"agent_outdated", "image_update_pending", "package_update_available",
		"pending_reboot", "repo_metadata_stale", "inventory_drift",
		"login_anomaly", "audit_action", "firewall_state_change",
		"fail2ban_jail_disappeared", "crowdsec_decisions":
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
//
// F-19: we deliberately drop e.quietMu around the DB query. Holding it would
// serialise every concurrent fire() through a single pool round-trip during
// a sustained outage. The TOCTOU race (two callers refresh in parallel,
// second overwrites first) is harmless — both reads return semantically
// identical configs, and 60 s of staleness in either direction is acceptable.
func (e *Engine) loadQuietConfig(ctx context.Context) *quietConfig {
	// Read cache under lock.
	e.quietMu.Lock()
	if e.quietCache != nil && time.Now().Before(e.quietExpiry) {
		c := e.quietCache
		e.quietMu.Unlock()
		return c
	}
	e.quietMu.Unlock()

	// Refresh outside the lock so a slow DB doesn't serialise alert dispatch.
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
		e.quietMu.Lock()
		e.quietCache = nil
		e.quietExpiry = time.Now().Add(quietCacheTTL)
		e.quietMu.Unlock()
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
	// Re-acquire to write. Last-writer-wins is fine — see comment above.
	e.quietMu.Lock()
	e.quietCache = cfg
	e.quietExpiry = time.Now().Add(quietCacheTTL)
	e.quietMu.Unlock()
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
	// F-5: bind seconds as int, not float64. make_interval(secs => $N)
	// requires an integer in PostgreSQL — within.Seconds() returns a float64
	// and pgx forwards it as numeric, which fails type resolution on the
	// `secs => integer` named arg. Casting at the call site keeps the
	// migration risk-free.
	withinSec := int(within.Seconds())
	if withinSec < 1 {
		withinSec = 1
	}
	err := e.Pool.QueryRow(ctx, `
		SELECT count(*) FROM alert_history
		WHERE dedup_key = $1
		  AND at >= now() - make_interval(secs => 86400)
		  AND at >= now() - make_interval(secs => $2)
		  AND coalesce(array_length(delivered_to, 1), 0) > 0`,
		dedup, withinSec).Scan(&n)
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
				// F-15: previously we degraded to an empty params map and
				// returned the rule anyway. For most evaluators that's fine
				// (they fall back to defaults), but audit_action with empty
				// params matches every audit_log row and would silently fire
				// on every event. Safer to skip the rule for this tick;
				// operators can fix the JSONB or delete the rule.
				slog.Error("alerts: rule disabled — bad condition_params",
					"rule_id", r.ID, "rule_name", r.Name, "err", err)
				continue
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
//
// F-17: results are cached for hostScopeTTL (60 s) keyed by host_id. A
// breach storm firing many rules against the same host now does one DB
// round-trip per host per minute instead of one per (rule, host) pair.
// An admin re-tagging a host sees the change on the next refresh, which
// is acceptable for an alerting cadence; if instant invalidation is ever
// required we can add an explicit InvalidateHostScope(hostID) hook.
func (e *Engine) fetchHostScope(ctx context.Context, hostID uuid.UUID) ([]string, []uuid.UUID) {
	now := time.Now()
	e.hostScopeMu.Lock()
	if entry, ok := e.hostScopeCache[hostID]; ok && now.Before(entry.expires) {
		tags, groups := entry.tags, entry.groupIDs
		e.hostScopeMu.Unlock()
		return tags, groups
	}
	e.hostScopeMu.Unlock()

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
	e.hostScopeMu.Lock()
	e.hostScopeCache[hostID] = hostScopeEntry{
		tags:     tags,
		groupIDs: groupIDs,
		expires:  now.Add(hostScopeTTL),
	}
	e.hostScopeMu.Unlock()
	return tags, groupIDs
}

// clampInt clamps v to the inclusive range [lo, hi]. Used to bound
// operator-supplied window/threshold params so a typoed rule (window_sec =
// 31536000) doesn't generate a year-long query window.
func clampInt(v, lo, hi int) int {
	if v < lo {
		return lo
	}
	if v > hi {
		return hi
	}
	return v
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

func floatParam(m map[string]any, key string, def float64) float64 {
	switch v := m[key].(type) {
	case float64:
		return v
	case int:
		return float64(v)
	}
	return def
}

func stringParam(m map[string]any, key, def string) string {
	if v, ok := m[key].(string); ok && v != "" {
		return v
	}
	return def
}

func boolParam(m map[string]any, key string, def bool) bool {
	if v, ok := m[key].(bool); ok {
		return v
	}
	return def
}

// mapParam extracts a nested string→string map from condition_params (e.g.
// the "scope" object used by metric_threshold). Returns an empty map on miss
// so callers can index without nil-checks.
func mapParam(m map[string]any, key string) map[string]string {
	out := map[string]string{}
	raw, ok := m[key].(map[string]any)
	if !ok {
		return out
	}
	for k, v := range raw {
		if s, ok := v.(string); ok {
			out[k] = s
		}
	}
	return out
}

// sqlComparator validates a comparator string from condition_params and
// returns the SQL-safe operator. Anything else returns "" — callers should
// skip the rule on empty.
func sqlComparator(cmp string) string {
	switch cmp {
	case ">", ">=", "<", "<=":
		return cmp
	}
	return ""
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

// --- metric_threshold evaluator -------------------------------------------
//
// metric_threshold is the generic "is the latest sample of <metric> on the
// <comparator> side of <value> sustained for <for_sec>?" check. It dispatches
// to a per-metric SQL query because each metric source has a different shape
// (per-host vs. per-mountpoint vs. counter requiring rate derivation).
//
// All queries follow the same skeleton:
//
//	SELECT host_id [, partition_key, max(time)]
//	FROM <hypertable>
//	WHERE time > now() - make_interval(secs => $1)
//	GROUP BY host_id [, partition_key]
//	HAVING bool_and(<metric_expr> <op> $2)
//	   AND count(*) >= 2
//
// We bind windowSec (or forSec when sustained-for is shorter than window)
// as $1 and the threshold as $2. bool_and() returns TRUE only when every
// sample in the bucket satisfies the predicate — that's the "sustained"
// semantics operators expect (one transient spike below threshold should
// not silence the alert, and one transient spike above should not fire it).
// count(*) >= 2 is a guard against firing on a single brand-new host where
// the very first sample happens to exceed the threshold.
func (e *Engine) evalMetricThreshold(ctx context.Context) {
	rules, err := e.loadRules(ctx, "metric_threshold")
	if err != nil {
		slog.Warn("alerts: load metric_threshold rules", "err", err)
		return
	}
	for _, r := range rules {
		metric := stringParam(r.ConditionParams, "metric", "")
		cmpStr := stringParam(r.ConditionParams, "comparator", ">")
		val := floatParam(r.ConditionParams, "value", 0)
		// F-5: clamp operator-supplied window/for_sec to sane bounds before
		// we plumb them into SQL. Lower bound 1 s (anything less wouldn't
		// generate enough samples for bool_and). Upper bound 24 h matches
		// the apitypes.MetricThresholdParams schema annotation.
		windowSec := clampInt(intParam(r.ConditionParams, "window_sec", 120), 1, 86400)
		forSec := clampInt(intParam(r.ConditionParams, "for_sec", windowSec), 1, windowSec)
		if forSec > windowSec {
			forSec = windowSec
		}
		scope := mapParam(r.ConditionParams, "scope")

		op := sqlComparator(cmpStr)
		if op == "" {
			slog.Warn("alerts: metric_threshold invalid comparator", "rule", r.Name, "cmp", cmpStr)
			continue
		}
		if metric == "" {
			slog.Warn("alerts: metric_threshold missing metric", "rule", r.Name)
			continue
		}
		e.runMetricRule(ctx, r, metric, op, val, windowSec, forSec, scope)
	}
}

// runMetricRule dispatches a single metric_threshold rule to the right
// per-metric query builder. The split keeps each metric's SQL self-contained
// (some need joins to disks/nics, some are cumulative counters that need a
// window function to compute a rate, some are one-shot lookups).
func (e *Engine) runMetricRule(ctx context.Context, r ruleRow, metric, op string, val float64, windowSec, forSec int, scope map[string]string) {
	switch metric {
	case "cpu_usage_pct":
		e.metricSimpleHost(ctx, r, metric, "metrics_system", "cpu_usage_pct", op, val, forSec)
	case "load_1":
		e.metricSimpleHost(ctx, r, metric, "metrics_system", "load_1", op, val, forSec)
	case "load_5":
		e.metricSimpleHost(ctx, r, metric, "metrics_system", "load_5", op, val, forSec)
	case "load_15":
		e.metricSimpleHost(ctx, r, metric, "metrics_system", "load_15", op, val, forSec)
	case "swap_used_bytes":
		e.metricSimpleHost(ctx, r, metric, "metrics_system", "swap_used_bytes::float", op, val, forSec)
	case "ram_used_pct":
		e.metricSimpleHost(ctx, r, metric, "metrics_system",
			"100.0 * ram_used_bytes::float / NULLIF(ram_used_bytes + ram_avail_bytes, 0)",
			op, val, forSec)
	case "swap_used_pct":
		// We don't have swap_total directly, but swap_used_pct only makes
		// sense relative to a known total. Approximation: swap_used_bytes
		// vs. ram_total_bytes from hosts table. Falls back to comparing
		// raw bytes if the host row is missing the total.
		e.metricSwapPct(ctx, r, op, val, forSec)
	case "cpu_per_core_pct":
		e.metricCPUPerCore(ctx, r, op, val, forSec)
	case "disk_used_pct":
		e.metricDiskExpr(ctx, r,
			"100.0 * used_bytes::float / NULLIF(used_bytes + free_bytes, 0)",
			op, val, forSec, scope["mountpoint"])
	case "disk_inode_used_pct":
		e.metricDiskExpr(ctx, r,
			"100.0 * inodes_used::float / NULLIF(inodes_used + inodes_free, 0)",
			op, val, forSec, scope["mountpoint"])
	case "disk_iops_total":
		e.metricDiskRate(ctx, r, "read_ops + write_ops", op, val, forSec, scope["mountpoint"])
	case "disk_io_util_pct":
		// io_time_ms is cumulative ms; util% in a window is
		// delta(io_time_ms) / delta(time_ms). bool_and across samples.
		e.metricDiskUtil(ctx, r, op, val, forSec, scope["mountpoint"])
	case "nic_rx_bytes_per_sec":
		e.metricNICRate(ctx, r, "rx_bytes", op, val, forSec, scope["nic"])
	case "nic_tx_bytes_per_sec":
		e.metricNICRate(ctx, r, "tx_bytes", op, val, forSec, scope["nic"])
	case "nic_err_per_sec":
		e.metricNICRate(ctx, r, "rx_errs + tx_errs", op, val, forSec, scope["nic"])
	case "nic_drop_per_sec":
		e.metricNICRate(ctx, r, "rx_drops + tx_drops", op, val, forSec, scope["nic"])
	case "workload_cpu_usage_pct":
		e.metricWorkloadExpr(ctx, r, "cpu_usage_pct", op, val, forSec, scope["workload_id"])
	case "workload_mem_used_pct":
		e.metricWorkloadExpr(ctx, r,
			"100.0 * mem_used_bytes::float / NULLIF(mem_limit_bytes, 0)",
			op, val, forSec, scope["workload_id"])
	case "fail2ban_currently_banned":
		e.metricFail2banBanned(ctx, r, op, val)
	case "crowdsec_active_decisions":
		e.metricCrowdSecActive(ctx, r, op, val)
	case "repo_metadata_age_sec":
		e.metricRepoMetadataAge(ctx, r, op, val)
	case "monitor_last_latency_ms":
		e.metricMonitorLatency(ctx, r, op, val, scope["monitor_id"])
	default:
		slog.Warn("alerts: metric_threshold unknown metric", "rule", r.Name, "metric", metric)
	}
}

// SQL-injection contract (F-1, F-2 defensive):
//
//   The `expr`, `valueExpr`, `table`, and `column` arguments threaded through
//   the per-metric helpers below (metricSimpleHost, metricSwapPct,
//   metricCPUPerCore, metricDiskExpr, metricDiskRate, metricDiskUtil,
//   metricNICRate, metricWorkloadExpr, metricFail2banBanned,
//   metricCrowdSecActive, metricRepoMetadataAge, metricMonitorLatency, and
//   driftScalarHost) are COMPILE-TIME CONSTANTS only. They are spliced into
//   the SQL via fmt.Sprintf because Postgres named-parameter binding does
//   not extend to identifiers, projections, or arithmetic expressions.
//
//   Future patches that try to plumb user-supplied expressions (e.g. a
//   "custom metric" feature reading expr from condition_params) would create
//   instant SQL injection. If a feature like that is ever needed:
//
//     - whitelist the operator-supplied tokens (column names against an
//       explicit allowlist queried from information_schema or a static
//       constant);
//     - or compile the expression server-side into an AST before splicing.
//
//   sqlComparator() is the existing model — it accepts only `>`, `>=`, `<`,
//   `<=` and returns "" for anything else; callers MUST check for "" before
//   splicing. Replicate that pattern.

// metricSimpleHost runs a HAVING bool_and(<expr> <op> $2) query over
// metrics_system for the given host-grain expression. Used for cpu/load/ram.
//
// expr/table: compile-time constants. See the SQL-injection contract above.
func (e *Engine) metricSimpleHost(ctx context.Context, r ruleRow, metric, table, expr, op string, val float64, forSec int) {
	sql := fmt.Sprintf(`
		SELECT host_id, max(time) AS last_t, count(*) AS samples
		FROM %s
		WHERE time > now() - make_interval(secs => $1)
		GROUP BY host_id
		HAVING bool_and(%s %s $2) AND count(*) >= 2`, table, expr, op)
	rows, err := e.Pool.Query(ctx, sql, forSec, val)
	if err != nil {
		slog.Warn("alerts: metric_threshold query", "metric", metric, "err", err)
		return
	}
	defer rows.Close()
	for rows.Next() {
		var hostID uuid.UUID
		var lastT time.Time
		var n int
		if err := rows.Scan(&hostID, &lastT, &n); err != nil {
			continue
		}
		e.fireMetricRule(ctx, r, hostID, metric, "", op, val, lastT)
	}
}

// metricSwapPct approximates swap_used_pct using hosts.ram_total_bytes as a
// stand-in for the (absent) swap-total column. Operators with no swap will
// never trip this rule because swap_used_bytes is 0 in that case.
//
// `op` is the only spliced fragment — already validated by sqlComparator
// to be one of {>, >=, <, <=}. See the SQL-injection contract above.
func (e *Engine) metricSwapPct(ctx context.Context, r ruleRow, op string, val float64, forSec int) {
	sql := fmt.Sprintf(`
		SELECT m.host_id, max(m.time), count(*)
		FROM metrics_system m JOIN hosts h ON h.id = m.host_id
		WHERE m.time > now() - make_interval(secs => $1)
		  AND h.ram_total_bytes > 0
		GROUP BY m.host_id
		HAVING bool_and(100.0 * m.swap_used_bytes::float / h.ram_total_bytes %s $2)
		   AND count(*) >= 2`, op)
	rows, err := e.Pool.Query(ctx, sql, forSec, val)
	if err != nil {
		slog.Warn("alerts: metric_threshold swap_used_pct query", "err", err)
		return
	}
	defer rows.Close()
	for rows.Next() {
		var hostID uuid.UUID
		var lastT time.Time
		var n int
		if err := rows.Scan(&hostID, &lastT, &n); err != nil {
			continue
		}
		e.fireMetricRule(ctx, r, hostID, "swap_used_pct", "", op, val, lastT)
	}
}

// metricCPUPerCore flags any sample whose cpu_per_core array contains a core
// matching the predicate. unnest() runs in an inline subquery to expose each
// core value as a row, and we then group back to host so the bool_and reads
// "every recent sample had at least one offending core".
//
// `op` is the only spliced fragment — already validated by sqlComparator.
// See the SQL-injection contract above.
func (e *Engine) metricCPUPerCore(ctx context.Context, r ruleRow, op string, val float64, forSec int) {
	sql := fmt.Sprintf(`
		SELECT host_id, max(time), count(*)
		FROM (
			SELECT host_id, time,
			       bool_or(c %s $2) AS any_core_match
			FROM metrics_system,
			     unnest(cpu_per_core) AS c
			WHERE time > now() - make_interval(secs => $1)
			GROUP BY host_id, time
		) s
		WHERE any_core_match
		GROUP BY host_id
		HAVING count(*) >= 2`, op)
	rows, err := e.Pool.Query(ctx, sql, forSec, val)
	if err != nil {
		slog.Warn("alerts: metric_threshold cpu_per_core query", "err", err)
		return
	}
	defer rows.Close()
	for rows.Next() {
		var hostID uuid.UUID
		var lastT time.Time
		var n int
		if err := rows.Scan(&hostID, &lastT, &n); err != nil {
			continue
		}
		e.fireMetricRule(ctx, r, hostID, "cpu_per_core_pct", "", op, val, lastT)
	}
}

// metricDiskExpr evaluates a per-mountpoint expression over metrics_disk
// joined with disks (so the alert subject can reference a human-readable
// mountpoint). When mountpoint != "" the rule narrows to a specific mount.
//
// expr: compile-time constant. See the SQL-injection contract above.
func (e *Engine) metricDiskExpr(ctx context.Context, r ruleRow, expr, op string, val float64, forSec int, mountpoint string) {
	args := []any{forSec, val}
	mpFilter := ""
	if mountpoint != "" {
		mpFilter = " AND d.mountpoint = $3"
		args = append(args, mountpoint)
	}
	sql := fmt.Sprintf(`
		SELECT m.host_id, d.mountpoint, max(m.time), count(*)
		FROM metrics_disk m JOIN disks d ON d.id = m.disk_id
		WHERE m.time > now() - make_interval(secs => $1)%s
		GROUP BY m.host_id, d.mountpoint
		HAVING bool_and(%s %s $2) AND count(*) >= 2`, mpFilter, expr, op)
	rows, err := e.Pool.Query(ctx, sql, args...)
	if err != nil {
		slog.Warn("alerts: metric_threshold disk query", "err", err)
		return
	}
	defer rows.Close()
	for rows.Next() {
		var hostID uuid.UUID
		var mp string
		var lastT time.Time
		var n int
		if err := rows.Scan(&hostID, &mp, &lastT, &n); err != nil {
			continue
		}
		e.fireMetricRule(ctx, r, hostID, "disk_used_pct", mp, op, val, lastT)
	}
}

// metricDiskRate derives a per-disk rate from a cumulative counter (read_ops,
// write_ops, …) using window-function lag(). Same bool_and pattern.
//
// valueExpr: compile-time constant. See the SQL-injection contract above.
//
// F-4: sample pairs spanning a counter reset (typically a host reboot, since
// these counters are cumulative since-boot) are elided via `v >= prev_v`.
// Without this guard the derived rate goes negative for one pair and trips
// the bool_and to FALSE, silently clearing a sustained breach. A breach that
// continues past a reboot will re-fire one tick later (once two samples have
// accumulated on the new boot session).
func (e *Engine) metricDiskRate(ctx context.Context, r ruleRow, valueExpr, op string, val float64, forSec int, mountpoint string) {
	args := []any{forSec, val}
	mpFilter := ""
	if mountpoint != "" {
		mpFilter = " AND d.mountpoint = $3"
		args = append(args, mountpoint)
	}
	sql := fmt.Sprintf(`
		WITH samples AS (
			SELECT m.host_id, d.mountpoint, m.time,
			       (%s)::float AS v,
			       lag((%s)::float) OVER (PARTITION BY m.host_id, m.disk_id ORDER BY m.time) AS prev_v,
			       lag(m.time)       OVER (PARTITION BY m.host_id, m.disk_id ORDER BY m.time) AS prev_t
			FROM metrics_disk m JOIN disks d ON d.id = m.disk_id
			WHERE m.time > now() - make_interval(secs => $1)%s
		)
		SELECT host_id, mountpoint, max(time), count(*)
		FROM samples
		WHERE prev_v IS NOT NULL
		  AND v >= prev_v
		  AND EXTRACT(EPOCH FROM (time - prev_t)) > 0
		GROUP BY host_id, mountpoint
		HAVING bool_and((v - prev_v) / EXTRACT(EPOCH FROM (time - prev_t)) %s $2)
		   AND count(*) >= 2`, valueExpr, valueExpr, mpFilter, op)
	rows, err := e.Pool.Query(ctx, sql, args...)
	if err != nil {
		slog.Warn("alerts: metric_threshold disk rate query", "err", err)
		return
	}
	defer rows.Close()
	for rows.Next() {
		var hostID uuid.UUID
		var mp string
		var lastT time.Time
		var n int
		if err := rows.Scan(&hostID, &mp, &lastT, &n); err != nil {
			continue
		}
		e.fireMetricRule(ctx, r, hostID, "disk_iops_total", mp, op, val, lastT)
	}
}

// metricDiskUtil computes io_time_ms delta / wall-clock-delta-ms across each
// adjacent sample pair. >100 is possible on multi-queue disks; we trust the
// operator-supplied threshold.
//
// F-4: see metricDiskRate; sample pairs spanning a counter reset are
// elided via `v >= prev_v`.
func (e *Engine) metricDiskUtil(ctx context.Context, r ruleRow, op string, val float64, forSec int, mountpoint string) {
	args := []any{forSec, val}
	mpFilter := ""
	if mountpoint != "" {
		mpFilter = " AND d.mountpoint = $3"
		args = append(args, mountpoint)
	}
	sql := fmt.Sprintf(`
		WITH samples AS (
			SELECT m.host_id, d.mountpoint, m.time,
			       m.io_time_ms::float AS v,
			       lag(m.io_time_ms::float) OVER (PARTITION BY m.host_id, m.disk_id ORDER BY m.time) AS prev_v,
			       lag(m.time)              OVER (PARTITION BY m.host_id, m.disk_id ORDER BY m.time) AS prev_t
			FROM metrics_disk m JOIN disks d ON d.id = m.disk_id
			WHERE m.time > now() - make_interval(secs => $1)%s
		)
		SELECT host_id, mountpoint, max(time), count(*)
		FROM samples
		WHERE prev_v IS NOT NULL
		  AND v >= prev_v
		  AND EXTRACT(EPOCH FROM (time - prev_t)) > 0
		GROUP BY host_id, mountpoint
		HAVING bool_and(100.0 * (v - prev_v) / (1000.0 * EXTRACT(EPOCH FROM (time - prev_t))) %s $2)
		   AND count(*) >= 2`, mpFilter, op)
	rows, err := e.Pool.Query(ctx, sql, args...)
	if err != nil {
		slog.Warn("alerts: metric_threshold disk_io_util query", "err", err)
		return
	}
	defer rows.Close()
	for rows.Next() {
		var hostID uuid.UUID
		var mp string
		var lastT time.Time
		var n int
		if err := rows.Scan(&hostID, &mp, &lastT, &n); err != nil {
			continue
		}
		e.fireMetricRule(ctx, r, hostID, "disk_io_util_pct", mp, op, val, lastT)
	}
}

// metricNICRate computes a per-NIC byte/pkt/err/drop rate from the matching
// cumulative counter. Joins nics for a human-readable name.
//
// valueExpr: compile-time constant. See the SQL-injection contract above.
//
// F-4: sample pairs spanning a counter reset (host reboot or NIC reset that
// zeroes the byte/pkt counter) are elided via `v >= prev_v`. A sustained
// breach across a reboot will fire one tick later.
func (e *Engine) metricNICRate(ctx context.Context, r ruleRow, valueExpr, op string, val float64, forSec int, nicName string) {
	args := []any{forSec, val}
	nicFilter := ""
	if nicName != "" {
		nicFilter = " AND n.name = $3"
		args = append(args, nicName)
	}
	sql := fmt.Sprintf(`
		WITH samples AS (
			SELECT m.host_id, n.name AS nic_name, m.time,
			       (%s)::float AS v,
			       lag((%s)::float) OVER (PARTITION BY m.host_id, m.nic_id ORDER BY m.time) AS prev_v,
			       lag(m.time)       OVER (PARTITION BY m.host_id, m.nic_id ORDER BY m.time) AS prev_t
			FROM metrics_net m JOIN nics n ON n.id = m.nic_id
			WHERE m.time > now() - make_interval(secs => $1)%s
		)
		SELECT host_id, nic_name, max(time), count(*)
		FROM samples
		WHERE prev_v IS NOT NULL
		  AND v >= prev_v
		  AND EXTRACT(EPOCH FROM (time - prev_t)) > 0
		GROUP BY host_id, nic_name
		HAVING bool_and((v - prev_v) / EXTRACT(EPOCH FROM (time - prev_t)) %s $2)
		   AND count(*) >= 2`, valueExpr, valueExpr, nicFilter, op)
	rows, err := e.Pool.Query(ctx, sql, args...)
	if err != nil {
		slog.Warn("alerts: metric_threshold nic rate query", "err", err)
		return
	}
	defer rows.Close()
	for rows.Next() {
		var hostID uuid.UUID
		var nic string
		var lastT time.Time
		var n int
		if err := rows.Scan(&hostID, &nic, &lastT, &n); err != nil {
			continue
		}
		e.fireMetricRule(ctx, r, hostID, "nic_rate", nic, op, val, lastT)
	}
}

// metricWorkloadExpr evaluates a per-workload expression. scope.workload_id
// (the UUID) narrows the rule; without it every workload that breaches will
// fire independently (one dedup key per workload).
//
// expr: compile-time constant. See the SQL-injection contract above.
func (e *Engine) metricWorkloadExpr(ctx context.Context, r ruleRow, expr, op string, val float64, forSec int, workloadIDStr string) {
	args := []any{forSec, val}
	idFilter := ""
	if workloadIDStr != "" {
		if wid, err := uuid.Parse(workloadIDStr); err == nil {
			idFilter = " AND m.workload_id = $3"
			args = append(args, wid)
		}
	}
	sql := fmt.Sprintf(`
		SELECT m.host_id, m.workload_id, max(m.time), count(*)
		FROM metrics_workload m
		WHERE m.time > now() - make_interval(secs => $1)%s
		GROUP BY m.host_id, m.workload_id
		HAVING bool_and(%s %s $2) AND count(*) >= 2`, idFilter, expr, op)
	rows, err := e.Pool.Query(ctx, sql, args...)
	if err != nil {
		slog.Warn("alerts: metric_threshold workload query", "err", err)
		return
	}
	defer rows.Close()
	for rows.Next() {
		var hostID, wlID uuid.UUID
		var lastT time.Time
		var n int
		if err := rows.Scan(&hostID, &wlID, &lastT, &n); err != nil {
			continue
		}
		e.fireMetricRule(ctx, r, hostID, "workload", wlID.String(), op, val, lastT)
	}
}

// metricFail2banBanned compares the per-host sum(currently_banned) against
// the threshold. No time window: fail2ban_jails is a current-state table.
//
// `op` is the only spliced fragment — sqlComparator-validated. See the
// SQL-injection contract above.
func (e *Engine) metricFail2banBanned(ctx context.Context, r ruleRow, op string, val float64) {
	sql := fmt.Sprintf(`
		SELECT host_id, sum(currently_banned)::float
		FROM fail2ban_jails
		GROUP BY host_id
		HAVING sum(currently_banned)::float %s $1`, op)
	rows, err := e.Pool.Query(ctx, sql, val)
	if err != nil {
		slog.Warn("alerts: metric_threshold fail2ban_banned query", "err", err)
		return
	}
	defer rows.Close()
	for rows.Next() {
		var hostID uuid.UUID
		var v float64
		if err := rows.Scan(&hostID, &v); err != nil {
			continue
		}
		e.fireMetricRule(ctx, r, hostID, "fail2ban_currently_banned", "", op, val, time.Now())
	}
}

// metricCrowdSecActive counts active CrowdSec decisions per host.
//
// `op` is the only spliced fragment — sqlComparator-validated. See the
// SQL-injection contract above.
func (e *Engine) metricCrowdSecActive(ctx context.Context, r ruleRow, op string, val float64) {
	sql := fmt.Sprintf(`
		SELECT host_id, count(*)::float
		FROM crowdsec_decisions
		WHERE until > now()
		GROUP BY host_id
		HAVING count(*)::float %s $1`, op)
	rows, err := e.Pool.Query(ctx, sql, val)
	if err != nil {
		slog.Warn("alerts: metric_threshold crowdsec query", "err", err)
		return
	}
	defer rows.Close()
	for rows.Next() {
		var hostID uuid.UUID
		var v float64
		if err := rows.Scan(&hostID, &v); err != nil {
			continue
		}
		e.fireMetricRule(ctx, r, hostID, "crowdsec_active_decisions", "", op, val, time.Now())
	}
}

// metricRepoMetadataAge compares max(metadata_age_seconds) per (host,
// manager) against the threshold.
//
// `op` is the only spliced fragment — sqlComparator-validated. See the
// SQL-injection contract above.
func (e *Engine) metricRepoMetadataAge(ctx context.Context, r ruleRow, op string, val float64) {
	sql := fmt.Sprintf(`
		SELECT host_id, manager, max(metadata_age_seconds)::float
		FROM package_repo_state
		GROUP BY host_id, manager
		HAVING max(metadata_age_seconds)::float %s $1`, op)
	rows, err := e.Pool.Query(ctx, sql, val)
	if err != nil {
		slog.Warn("alerts: metric_threshold repo_metadata_age query", "err", err)
		return
	}
	defer rows.Close()
	for rows.Next() {
		var hostID uuid.UUID
		var mgr string
		var v float64
		if err := rows.Scan(&hostID, &mgr, &v); err != nil {
			continue
		}
		e.fireMetricRule(ctx, r, hostID, "repo_metadata_age_sec", mgr, op, val, time.Now())
	}
}

// metricMonitorLatency reads monitors.last_latency_ms (one-shot, not the
// hypertable). monitor_id scope is mandatory in practice — without it the
// alert fires once per monitor and the dedup key gets fuzzy.
//
// `op` is the only spliced fragment — sqlComparator-validated. See the
// SQL-injection contract above.
func (e *Engine) metricMonitorLatency(ctx context.Context, r ruleRow, op string, val float64, monitorIDStr string) {
	args := []any{val}
	idFilter := ""
	if monitorIDStr != "" {
		if mid, err := uuid.Parse(monitorIDStr); err == nil {
			idFilter = " AND id = $2"
			args = append(args, mid)
		}
	}
	sql := fmt.Sprintf(`
		SELECT id, name, last_latency_ms
		FROM monitors
		WHERE enabled = TRUE AND last_latency_ms IS NOT NULL
		  AND last_latency_ms::float %s $1%s`, op, idFilter)
	rows, err := e.Pool.Query(ctx, sql, args...)
	if err != nil {
		slog.Warn("alerts: metric_threshold monitor_latency query", "err", err)
		return
	}
	defer rows.Close()
	for rows.Next() {
		var mid uuid.UUID
		var name string
		var latency int
		if err := rows.Scan(&mid, &name, &latency); err != nil {
			continue
		}
		dedup := fmt.Sprintf("metric_threshold:%s:%s", mid, "monitor_latency")
		subj := fmt.Sprintf("[MonSys] monitor %s latency %dms %s %.0f", name, latency, op, val)
		body := fmt.Sprintf("Monitor %s last latency %dms %s threshold %.0fms.", name, latency, op, val)
		e.fire(ctx, r, subj, body, dedup)
	}
}

// fireMetricRule is the common fire path for the per-host metric_threshold
// evaluators. host-scope matching uses fetchHostScope, and the dedup key
// embeds metric + scope so distinct mountpoints/NICs/workloads on the same
// host don't share an alert_state row.
func (e *Engine) fireMetricRule(ctx context.Context, r ruleRow, hostID uuid.UUID, metric, scopeKey, op string, val float64, lastT time.Time) {
	tags, groupIDs := e.fetchHostScope(ctx, hostID)
	if !r.matchesHost(hostID, tags, groupIDs) {
		return
	}
	scopeSuffix := ""
	if scopeKey != "" {
		scopeSuffix = ":" + scopeKey
	}
	dedup := fmt.Sprintf("metric_threshold:%s:%s%s", hostID, metric, scopeSuffix)
	scopeStr := ""
	if scopeKey != "" {
		scopeStr = fmt.Sprintf(" (%s)", scopeKey)
	}
	subj := fmt.Sprintf("[MonSys] %s%s %s %.2f", metric, scopeStr, op, val)
	body := fmt.Sprintf("Host %s sustained %s%s %s %.2f (last sample %s).",
		hostID, metric, scopeStr, op, val, lastT.UTC().Format(time.RFC3339))
	e.fire(ctx, r, subj, body, dedup)
}

// --- unexpected_reboot ----------------------------------------------------
//
// Compares each host's two most recent uptime_sec samples. A non-rolled-over
// reset (newer < older - 30s grace) indicates the host rebooted between
// reports. Cooldown of 1h prevents the same boot from firing repeatedly while
// the older sample slides out of the ranking window.
func (e *Engine) evalUnexpectedReboot(ctx context.Context) {
	rules, err := e.loadRules(ctx, "unexpected_reboot")
	if err != nil || len(rules) == 0 {
		if err != nil {
			slog.Warn("alerts: load unexpected_reboot rules", "err", err)
		}
		return
	}
	sql := `
		WITH ranked AS (
			SELECT host_id, uptime_sec, time,
			       row_number() OVER (PARTITION BY host_id ORDER BY time DESC) AS rn
			FROM metrics_system
			WHERE time > now() - interval '2 hours'
		)
		SELECT a.host_id, a.uptime_sec AS new_uptime, b.uptime_sec AS old_uptime, a.time
		FROM ranked a JOIN ranked b ON a.host_id = b.host_id
		WHERE a.rn = 1 AND b.rn = 2
		  AND a.uptime_sec IS NOT NULL AND b.uptime_sec IS NOT NULL
		  AND a.uptime_sec < b.uptime_sec - 30`
	rows, err := e.Pool.Query(ctx, sql)
	if err != nil {
		slog.Warn("alerts: unexpected_reboot query", "err", err)
		return
	}
	defer rows.Close()
	type hit struct {
		hostID uuid.UUID
		newUp  int64
		oldUp  int64
		at     time.Time
	}
	var hits []hit
	for rows.Next() {
		var h hit
		if err := rows.Scan(&h.hostID, &h.newUp, &h.oldUp, &h.at); err != nil {
			continue
		}
		hits = append(hits, h)
	}
	now := time.Now()
	for _, h := range hits {
		e.stateMu.Lock()
		last, ok := e.unexpReboot[h.hostID]
		e.stateMu.Unlock()
		if ok && now.Sub(last) < time.Hour {
			continue
		}
		for _, r := range rules {
			tags, groupIDs := e.fetchHostScope(ctx, h.hostID)
			if !r.matchesHost(h.hostID, tags, groupIDs) {
				continue
			}
			dedup := fmt.Sprintf("unexpected_reboot:%s", h.hostID)
			subj := fmt.Sprintf("[MonSys] host %s rebooted unexpectedly", h.hostID)
			body := fmt.Sprintf(
				"Host %s uptime_sec dropped from %d to %d at %s.",
				h.hostID, h.oldUp, h.newUp, h.at.UTC().Format(time.RFC3339))
			e.fire(ctx, r, subj, body, dedup)
		}
		e.stateMu.Lock()
		e.unexpReboot[h.hostID] = now
		e.stateMu.Unlock()
	}
}

// --- host_flap ------------------------------------------------------------
func (e *Engine) evalHostFlap(ctx context.Context) {
	rules, err := e.loadRules(ctx, "host_flap")
	if err != nil || len(rules) == 0 {
		if err != nil {
			slog.Warn("alerts: load host_flap rules", "err", err)
		}
		return
	}
	for _, r := range rules {
		// F-5: clamp to apitypes.HostFlapParams [60, 86400] bounds.
		windowSec := clampInt(intParam(r.ConditionParams, "window_sec", 1800), 60, 86400)
		threshold := intParam(r.ConditionParams, "threshold", 6)
		rows, err := e.Pool.Query(ctx, `
			SELECT h.id, h.hostname, count(*)
			FROM host_status_history hh JOIN hosts h ON h.id = hh.host_id
			WHERE hh.at > now() - make_interval(secs => $1)
			GROUP BY h.id, h.hostname
			HAVING count(*) > $2`, windowSec, threshold)
		if err != nil {
			slog.Warn("alerts: host_flap query", "err", err)
			continue
		}
		for rows.Next() {
			var hostID uuid.UUID
			var hostname string
			var n int
			if err := rows.Scan(&hostID, &hostname, &n); err != nil {
				continue
			}
			tags, groupIDs := e.fetchHostScope(ctx, hostID)
			if !r.matchesHost(hostID, tags, groupIDs) {
				continue
			}
			dedup := fmt.Sprintf("host_flap:%s", hostID)
			subj := fmt.Sprintf("[MonSys] host %s is flapping (%d transitions in %ds)", hostname, n, windowSec)
			body := fmt.Sprintf("Host %s recorded %d liveness transitions in the last %ds (threshold %d).",
				hostname, n, windowSec, threshold)
			e.fire(ctx, r, subj, body, dedup)
		}
		rows.Close()
	}
}

// --- container_state_change ----------------------------------------------
//
// State-change detection. We snapshot every workload's current state at each
// tick, diff against the in-memory cache, and fire only on transitions INTO
// the configured "bad" set. The first tick after engine boot seeds the cache
// without firing (matches prior state) so a long-running already-exited
// container doesn't alarm at restart.
func (e *Engine) evalContainerStateChange(ctx context.Context) {
	rules, err := e.loadRules(ctx, "container_state_change")
	if err != nil || len(rules) == 0 {
		if err != nil {
			slog.Warn("alerts: load container_state_change rules", "err", err)
		}
		return
	}
	rows, err := e.Pool.Query(ctx, `
		SELECT host_id, external_id, name, image, state
		FROM workloads
		WHERE state IS NOT NULL`)
	if err != nil {
		slog.Warn("alerts: container_state_change query", "err", err)
		return
	}
	defer rows.Close()
	type cs struct {
		hostID           uuid.UUID
		externalID, name string
		image, state     string
	}
	var snapshot []cs
	for rows.Next() {
		var c cs
		if err := rows.Scan(&c.hostID, &c.externalID, &c.name, &c.image, &c.state); err != nil {
			continue
		}
		snapshot = append(snapshot, c)
	}
	for _, c := range snapshot {
		key := c.hostID.String() + ":" + c.externalID
		e.stateMu.Lock()
		prev, seen := e.containerState[key]
		e.containerState[key] = c.state
		e.stateMu.Unlock()
		if !seen || prev == c.state {
			continue
		}
		for _, r := range rules {
			states := stringSliceParam(r.ConditionParams, "states", []string{"exited", "dead"})
			exclude := stringParam(r.ConditionParams, "exclude_image_substring", "")
			if !contains(states, c.state) {
				continue
			}
			if exclude != "" && c.image != "" && strings.Contains(c.image, exclude) {
				continue
			}
			tags, groupIDs := e.fetchHostScope(ctx, c.hostID)
			if !r.matchesHost(c.hostID, tags, groupIDs) {
				continue
			}
			dedup := fmt.Sprintf("container_state:%s:%s", c.hostID, c.externalID)
			subj := fmt.Sprintf("[MonSys] container %s entered %s", c.name, c.state)
			body := fmt.Sprintf("Container %q (%s) on host %s transitioned %s → %s.",
				c.name, c.image, c.hostID, prev, c.state)
			e.fire(ctx, r, subj, body, dedup)
		}
	}
}

// --- vm_state_change ------------------------------------------------------
func (e *Engine) evalVMStateChange(ctx context.Context) {
	rules, err := e.loadRules(ctx, "vm_state_change")
	if err != nil || len(rules) == 0 {
		if err != nil {
			slog.Warn("alerts: load vm_state_change rules", "err", err)
		}
		return
	}
	rows, err := e.Pool.Query(ctx, `
		SELECT host_id, external_id, name, kind, state, autostart
		FROM vms`)
	if err != nil {
		slog.Warn("alerts: vm_state_change query", "err", err)
		return
	}
	defer rows.Close()
	type vs struct {
		hostID                 uuid.UUID
		externalID, name, kind string
		state                  string
		autostart              bool
	}
	var snapshot []vs
	for rows.Next() {
		var v vs
		var autoNull *bool
		if err := rows.Scan(&v.hostID, &v.externalID, &v.name, &v.kind, &v.state, &autoNull); err != nil {
			continue
		}
		if autoNull != nil {
			v.autostart = *autoNull
		}
		snapshot = append(snapshot, v)
	}
	for _, v := range snapshot {
		key := v.hostID.String() + ":" + v.externalID
		e.stateMu.Lock()
		prev, seen := e.vmState[key]
		e.vmState[key] = v.state
		e.stateMu.Unlock()
		for _, r := range rules {
			subkind := stringParam(r.ConditionParams, "subkind", "any_transition")
			switch subkind {
			case "stopped":
				if !seen || prev == v.state {
					continue
				}
				if v.state == "running" || v.state == prev {
					continue
				}
				if v.state != "stopped" && v.state != "paused" && v.state != "shutdown" {
					continue
				}
			case "autostart_violation":
				if !v.autostart || v.state == "running" {
					continue
				}
				// Fire on persistent violation, but dedup so it
				// doesn't refire every tick.
			default: // any_transition
				if !seen || prev == v.state {
					continue
				}
			}
			tags, groupIDs := e.fetchHostScope(ctx, v.hostID)
			if !r.matchesHost(v.hostID, tags, groupIDs) {
				continue
			}
			dedup := fmt.Sprintf("vm_state:%s:%s:%s", v.hostID, v.externalID, subkind)
			subj := fmt.Sprintf("[MonSys] VM %s (%s): %s", v.name, v.kind, v.state)
			body := fmt.Sprintf("VM %s (%s/%s) on host %s state=%s (prev=%s, autostart=%t).",
				v.name, v.kind, v.externalID, v.hostID, v.state, prev, v.autostart)
			e.fire(ctx, r, subj, body, dedup)
		}
	}
}

// --- nic_link_down --------------------------------------------------------
func (e *Engine) evalNICLinkDown(ctx context.Context) {
	rules, err := e.loadRules(ctx, "nic_link_down")
	if err != nil || len(rules) == 0 {
		if err != nil {
			slog.Warn("alerts: load nic_link_down rules", "err", err)
		}
		return
	}
	rows, err := e.Pool.Query(ctx, `
		SELECT host_id, name, COALESCE(speed_mbps, 0)
		FROM nics`)
	if err != nil {
		slog.Warn("alerts: nic_link_down query", "err", err)
		return
	}
	defer rows.Close()
	type ns struct {
		hostID uuid.UUID
		name   string
		speed  int
	}
	var snapshot []ns
	for rows.Next() {
		var n ns
		if err := rows.Scan(&n.hostID, &n.name, &n.speed); err != nil {
			continue
		}
		snapshot = append(snapshot, n)
	}
	for _, n := range snapshot {
		key := n.hostID.String() + ":" + n.name
		e.stateMu.Lock()
		prev, seen := e.nicSpeed[key]
		e.nicSpeed[key] = n.speed
		e.stateMu.Unlock()
		for _, r := range rules {
			excludeLo := boolParam(r.ConditionParams, "exclude_loopback", true)
			excludeVirt := boolParam(r.ConditionParams, "exclude_virtual", true)
			if excludeLo && n.name == "lo" {
				continue
			}
			if excludeVirt && (strings.HasPrefix(n.name, "veth") ||
				strings.HasPrefix(n.name, "docker") ||
				strings.HasPrefix(n.name, "br-") ||
				strings.HasPrefix(n.name, "virbr") ||
				strings.HasPrefix(n.name, "tap")) {
				continue
			}
			if !seen || prev == 0 || n.speed != 0 {
				continue
			}
			tags, groupIDs := e.fetchHostScope(ctx, n.hostID)
			if !r.matchesHost(n.hostID, tags, groupIDs) {
				continue
			}
			dedup := fmt.Sprintf("nic_link_down:%s:%s", n.hostID, n.name)
			subj := fmt.Sprintf("[MonSys] NIC %s is down on %s", n.name, n.hostID)
			body := fmt.Sprintf("NIC %s on host %s reported speed %d Mbps (previous: %d).",
				n.name, n.hostID, n.speed, prev)
			e.fire(ctx, r, subj, body, dedup)
		}
	}
}

// --- nic_bond_degraded ----------------------------------------------------
func (e *Engine) evalNICBondDegraded(ctx context.Context) {
	rules, err := e.loadRules(ctx, "nic_bond_degraded")
	if err != nil || len(rules) == 0 {
		if err != nil {
			slog.Warn("alerts: load nic_bond_degraded rules", "err", err)
		}
		return
	}
	rows, err := e.Pool.Query(ctx, `
		SELECT host_id, name, COALESCE(array_length(members, 1), 0)
		FROM nics
		WHERE COALESCE(array_length(members, 1), 0) > 0`)
	if err != nil {
		slog.Warn("alerts: nic_bond_degraded query", "err", err)
		return
	}
	defer rows.Close()
	type bs struct {
		hostID  uuid.UUID
		name    string
		members int
	}
	var snapshot []bs
	for rows.Next() {
		var b bs
		if err := rows.Scan(&b.hostID, &b.name, &b.members); err != nil {
			continue
		}
		snapshot = append(snapshot, b)
	}
	for _, b := range snapshot {
		key := b.hostID.String() + ":" + b.name
		e.stateMu.Lock()
		prev, seen := e.nicMembers[key]
		// Baseline is the max seen so far — bond growth shouldn't downgrade
		// the alert threshold for future shrinks.
		if !seen || b.members > prev {
			e.nicMembers[key] = b.members
		}
		baseline := e.nicMembers[key]
		e.stateMu.Unlock()
		if !seen || b.members >= baseline {
			continue
		}
		for _, r := range rules {
			tags, groupIDs := e.fetchHostScope(ctx, b.hostID)
			if !r.matchesHost(b.hostID, tags, groupIDs) {
				continue
			}
			dedup := fmt.Sprintf("nic_bond_degraded:%s:%s", b.hostID, b.name)
			subj := fmt.Sprintf("[MonSys] bond/bridge %s degraded on %s", b.name, b.hostID)
			body := fmt.Sprintf("Bond %s on host %s has %d members (baseline %d).",
				b.name, b.hostID, b.members, baseline)
			e.fire(ctx, r, subj, body, dedup)
		}
	}
}

// --- agent_outdated -------------------------------------------------------
//
// When min_version is empty we derive a baseline from the freshest host's
// agent_version. Simple lex compare — semver-aware ordering can come later.
func (e *Engine) evalAgentOutdated(ctx context.Context) {
	rules, err := e.loadRules(ctx, "agent_outdated")
	if err != nil || len(rules) == 0 {
		if err != nil {
			slog.Warn("alerts: load agent_outdated rules", "err", err)
		}
		return
	}
	for _, r := range rules {
		minVer := stringParam(r.ConditionParams, "min_version", "")
		if minVer == "" {
			if err := e.Pool.QueryRow(ctx,
				`SELECT COALESCE(max(agent_version), '') FROM hosts WHERE agent_version IS NOT NULL`).
				Scan(&minVer); err != nil {
				slog.Warn("alerts: agent_outdated baseline lookup", "err", err)
				continue
			}
		}
		if minVer == "" {
			continue
		}
		rows, err := e.Pool.Query(ctx, `
			SELECT id, hostname, COALESCE(agent_version, '')
			FROM hosts
			WHERE agent_version IS NOT NULL
			  AND agent_version <> ''
			  AND agent_version < $1
			  AND last_seen_at > now() - interval '1 day'`, minVer)
		if err != nil {
			slog.Warn("alerts: agent_outdated query", "err", err)
			continue
		}
		for rows.Next() {
			var hostID uuid.UUID
			var hostname, ver string
			if err := rows.Scan(&hostID, &hostname, &ver); err != nil {
				continue
			}
			tags, groupIDs := e.fetchHostScope(ctx, hostID)
			if !r.matchesHost(hostID, tags, groupIDs) {
				continue
			}
			dedup := fmt.Sprintf("agent_outdated:%s", hostID)
			subj := fmt.Sprintf("[MonSys] agent on %s is outdated (%s < %s)", hostname, ver, minVer)
			body := fmt.Sprintf("Host %s reports agent version %q; baseline is %q.", hostname, ver, minVer)
			e.fire(ctx, r, subj, body, dedup)
		}
		rows.Close()
	}
}

// --- image_update_pending -------------------------------------------------
func (e *Engine) evalImageUpdatePending(ctx context.Context) {
	rules, err := e.loadRules(ctx, "image_update_pending")
	if err != nil || len(rules) == 0 {
		if err != nil {
			slog.Warn("alerts: load image_update_pending rules", "err", err)
		}
		return
	}
	for _, r := range rules {
		// F-5: clamp min_age_hours to [1, 720] (30 days).
		minAge := clampInt(intParam(r.ConditionParams, "min_age_hours", 24), 1, 720)
		rows, err := e.Pool.Query(ctx, `
			SELECT host_id, external_id, COALESCE(name, ''), COALESCE(image, ''), update_checked_at
			FROM workloads
			WHERE update_available = TRUE
			  AND update_checked_at IS NOT NULL
			  AND update_checked_at < now() - make_interval(hours => $1)`, minAge)
		if err != nil {
			slog.Warn("alerts: image_update_pending query", "err", err)
			continue
		}
		for rows.Next() {
			var hostID uuid.UUID
			var extID, name, image string
			var checkedAt time.Time
			if err := rows.Scan(&hostID, &extID, &name, &image, &checkedAt); err != nil {
				continue
			}
			tags, groupIDs := e.fetchHostScope(ctx, hostID)
			if !r.matchesHost(hostID, tags, groupIDs) {
				continue
			}
			dedup := fmt.Sprintf("image_update_pending:%s:%s", hostID, extID)
			subj := fmt.Sprintf("[MonSys] container %s on %s has an image update", name, hostID)
			body := fmt.Sprintf("Container %s (%s) on host %s has an available image update since %s.",
				name, image, hostID, checkedAt.UTC().Format(time.RFC3339))
			e.fire(ctx, r, subj, body, dedup)
		}
		rows.Close()
	}
}

// --- package_update_available ---------------------------------------------
func (e *Engine) evalPackageUpdateAvailable(ctx context.Context) {
	rules, err := e.loadRules(ctx, "package_update_available")
	if err != nil || len(rules) == 0 {
		if err != nil {
			slog.Warn("alerts: load package_update_available rules", "err", err)
		}
		return
	}
	for _, r := range rules {
		threshold := intParam(r.ConditionParams, "threshold", 50)
		rows, err := e.Pool.Query(ctx, `
			SELECT DISTINCT ON (host_id) host_id, updates_count, time
			FROM metrics_packages_summary
			ORDER BY host_id, time DESC`)
		if err != nil {
			slog.Warn("alerts: package_update_available query", "err", err)
			continue
		}
		for rows.Next() {
			var hostID uuid.UUID
			var cnt int
			var t time.Time
			if err := rows.Scan(&hostID, &cnt, &t); err != nil {
				continue
			}
			if cnt <= threshold {
				continue
			}
			tags, groupIDs := e.fetchHostScope(ctx, hostID)
			if !r.matchesHost(hostID, tags, groupIDs) {
				continue
			}
			dedup := fmt.Sprintf("package_update_available:%s", hostID)
			subj := fmt.Sprintf("[MonSys] host %s: %d packages need updating", hostID, cnt)
			body := fmt.Sprintf("Host %s has %d pending package updates (threshold %d, sampled at %s).",
				hostID, cnt, threshold, t.UTC().Format(time.RFC3339))
			e.fire(ctx, r, subj, body, dedup)
		}
		rows.Close()
	}
}

// --- pending_reboot -------------------------------------------------------
//
// Fires per host while a kernel package update is pending. The host_status
// LEFT JOIN auto-resolves once the kernel package row disappears (handled
// implicitly: nothing in the query returns the host, so the dedup key sees
// no re-fire and the open alert_state row can be closed by the next periodic
// pass — see explicit resolve below).
func (e *Engine) evalPendingReboot(ctx context.Context) {
	rules, err := e.loadRules(ctx, "pending_reboot")
	if err != nil || len(rules) == 0 {
		if err != nil {
			slog.Warn("alerts: load pending_reboot rules", "err", err)
		}
		return
	}
	rows, err := e.Pool.Query(ctx, `
		SELECT DISTINCT host_id, string_agg(name, ', ') AS pkgs
		FROM package_updates
		WHERE name LIKE 'linux-image%'
		   OR name LIKE 'linux-headers%'
		   OR name LIKE 'kernel%'
		   OR name LIKE 'linux-%'
		GROUP BY host_id`)
	if err != nil {
		slog.Warn("alerts: pending_reboot query", "err", err)
		return
	}
	defer rows.Close()
	active := map[uuid.UUID]string{}
	for rows.Next() {
		var hostID uuid.UUID
		var pkgs string
		if err := rows.Scan(&hostID, &pkgs); err != nil {
			continue
		}
		active[hostID] = pkgs
	}
	for hostID, pkgs := range active {
		for _, r := range rules {
			tags, groupIDs := e.fetchHostScope(ctx, hostID)
			if !r.matchesHost(hostID, tags, groupIDs) {
				continue
			}
			dedup := fmt.Sprintf("pending_reboot:%s", hostID)
			subj := fmt.Sprintf("[MonSys] host %s needs a reboot", hostID)
			body := fmt.Sprintf("Host %s has kernel package(s) pending update: %s.", hostID, pkgs)
			e.fire(ctx, r, subj, body, dedup)
		}
	}
	// Resolve hosts that previously had a pending-reboot open alert but no
	// kernel package update anymore. We scan alert_state and call resolve()
	// for each closed dedup key.
	resRows, err := e.Pool.Query(ctx, `
		SELECT s.dedup_key, s.host_id FROM alert_state s
		WHERE s.dedup_key LIKE 'pending_reboot:%' AND s.resolved_at IS NULL`)
	if err != nil {
		return
	}
	defer resRows.Close()
	for resRows.Next() {
		var dedup string
		var hostID *uuid.UUID
		if err := resRows.Scan(&dedup, &hostID); err != nil {
			continue
		}
		if hostID == nil {
			continue
		}
		if _, stillPending := active[*hostID]; !stillPending {
			e.resolve(ctx, dedup, fmt.Sprintf("Kernel package(s) on host %s no longer pending.", *hostID))
		}
	}
}

// --- repo_metadata_stale --------------------------------------------------
func (e *Engine) evalRepoMetadataStale(ctx context.Context) {
	rules, err := e.loadRules(ctx, "repo_metadata_stale")
	if err != nil || len(rules) == 0 {
		if err != nil {
			slog.Warn("alerts: load repo_metadata_stale rules", "err", err)
		}
		return
	}
	for _, r := range rules {
		// F-5: clamp threshold_sec to [60 s, 30 days].
		threshold := clampInt(intParam(r.ConditionParams, "threshold_sec", 86400), 60, 2_592_000)
		rows, err := e.Pool.Query(ctx, `
			SELECT host_id, manager, metadata_age_seconds
			FROM package_repo_state
			WHERE metadata_age_seconds > $1`, threshold)
		if err != nil {
			slog.Warn("alerts: repo_metadata_stale query", "err", err)
			continue
		}
		for rows.Next() {
			var hostID uuid.UUID
			var mgr string
			var age int64
			if err := rows.Scan(&hostID, &mgr, &age); err != nil {
				continue
			}
			tags, groupIDs := e.fetchHostScope(ctx, hostID)
			if !r.matchesHost(hostID, tags, groupIDs) {
				continue
			}
			dedup := fmt.Sprintf("repo_metadata_stale:%s:%s", hostID, mgr)
			subj := fmt.Sprintf("[MonSys] %s repo metadata is stale on %s", mgr, hostID)
			body := fmt.Sprintf("Host %s manager %s metadata age %ds > threshold %ds.",
				hostID, mgr, age, threshold)
			e.fire(ctx, r, subj, body, dedup)
		}
		rows.Close()
	}
}

// --- inventory_drift ------------------------------------------------------
//
// Per-subkind diff against the in-memory baseline. First tick after boot
// rebuilds the baseline silently; subsequent ticks fire on novel entries.
// Removals (e.g. NIC disappears) intentionally do NOT trigger inventory_drift
// — those are covered by nic_link_down/nic_bond_degraded.
func (e *Engine) evalInventoryDrift(ctx context.Context) {
	rules, err := e.loadRules(ctx, "inventory_drift")
	if err != nil || len(rules) == 0 {
		if err != nil {
			slog.Warn("alerts: load inventory_drift rules", "err", err)
		}
		return
	}
	for _, r := range rules {
		kind := stringParam(r.ConditionParams, "kind", "")
		switch kind {
		case "new_user":
			e.driftNewUser(ctx, r, false)
		case "new_sudoer":
			e.driftNewUser(ctx, r, true)
		case "new_disk":
			e.driftNewDisk(ctx, r)
		case "new_nic":
			e.driftNewNic(ctx, r)
		case "mac_changed":
			e.driftMACChanged(ctx, r)
		case "kernel_changed":
			e.driftScalarHost(ctx, r, "kernel", e.invKernel)
		case "distro_changed":
			e.driftScalarHost(ctx, r, "distro", e.invDistro)
		default:
			// "new_package" / "removed_package" intentionally not implemented:
			// packages cardinality is high and the security_updates_pending /
			// package_update_available rules already cover the alarming case.
			slog.Warn("alerts: inventory_drift unsupported kind", "rule", r.Name, "kind", kind)
		}
	}
}

func (e *Engine) driftNewUser(ctx context.Context, r ruleRow, sudoerOnly bool) {
	filter := ""
	if sudoerOnly {
		filter = " WHERE is_sudoer = TRUE"
	}
	rows, err := e.Pool.Query(ctx, `SELECT host_id, username FROM observed_users`+filter)
	if err != nil {
		slog.Warn("alerts: inventory_drift users query", "err", err)
		return
	}
	defer rows.Close()
	current := map[uuid.UUID]map[string]bool{}
	for rows.Next() {
		var hostID uuid.UUID
		var u string
		if err := rows.Scan(&hostID, &u); err != nil {
			continue
		}
		if current[hostID] == nil {
			current[hostID] = map[string]bool{}
		}
		current[hostID][u] = true
	}
	e.stateMu.Lock()
	defer e.stateMu.Unlock()
	cache := e.invUsers
	if sudoerOnly {
		cache = e.invSudoers
	}
	for hostID, set := range current {
		prev, seen := cache[hostID]
		if !seen {
			cache[hostID] = set
			continue
		}
		for u := range set {
			if prev[u] {
				continue
			}
			tags, groupIDs := e.fetchHostScope(ctx, hostID)
			if !r.matchesHost(hostID, tags, groupIDs) {
				continue
			}
			label := "user"
			if sudoerOnly {
				label = "sudoer"
			}
			dedup := fmt.Sprintf("inventory_drift:%s:%s:%s", hostID, label, u)
			subj := fmt.Sprintf("[MonSys] new %s %s on %s", label, u, hostID)
			body := fmt.Sprintf("New %s %q appeared on host %s.", label, u, hostID)
			e.fire(ctx, r, subj, body, dedup)
		}
		cache[hostID] = set
	}
}

func (e *Engine) driftNewDisk(ctx context.Context, r ruleRow) {
	rows, err := e.Pool.Query(ctx, `SELECT host_id, mountpoint FROM disks`)
	if err != nil {
		slog.Warn("alerts: inventory_drift disks query", "err", err)
		return
	}
	defer rows.Close()
	current := map[uuid.UUID]map[string]bool{}
	for rows.Next() {
		var hostID uuid.UUID
		var mp string
		if err := rows.Scan(&hostID, &mp); err != nil {
			continue
		}
		if current[hostID] == nil {
			current[hostID] = map[string]bool{}
		}
		current[hostID][mp] = true
	}
	e.stateMu.Lock()
	defer e.stateMu.Unlock()
	for hostID, set := range current {
		prev, seen := e.invDisks[hostID]
		if !seen {
			e.invDisks[hostID] = set
			continue
		}
		for mp := range set {
			if prev[mp] {
				continue
			}
			tags, groupIDs := e.fetchHostScope(ctx, hostID)
			if !r.matchesHost(hostID, tags, groupIDs) {
				continue
			}
			dedup := fmt.Sprintf("inventory_drift:%s:disk:%s", hostID, mp)
			subj := fmt.Sprintf("[MonSys] new disk %s on %s", mp, hostID)
			body := fmt.Sprintf("New disk mountpoint %q appeared on host %s.", mp, hostID)
			e.fire(ctx, r, subj, body, dedup)
		}
		e.invDisks[hostID] = set
	}
}

func (e *Engine) driftNewNic(ctx context.Context, r ruleRow) {
	rows, err := e.Pool.Query(ctx, `SELECT host_id, name FROM nics`)
	if err != nil {
		slog.Warn("alerts: inventory_drift nics query", "err", err)
		return
	}
	defer rows.Close()
	current := map[uuid.UUID]map[string]bool{}
	for rows.Next() {
		var hostID uuid.UUID
		var n string
		if err := rows.Scan(&hostID, &n); err != nil {
			continue
		}
		if current[hostID] == nil {
			current[hostID] = map[string]bool{}
		}
		current[hostID][n] = true
	}
	e.stateMu.Lock()
	defer e.stateMu.Unlock()
	for hostID, set := range current {
		prev, seen := e.invNics[hostID]
		if !seen {
			e.invNics[hostID] = set
			continue
		}
		for n := range set {
			if prev[n] {
				continue
			}
			tags, groupIDs := e.fetchHostScope(ctx, hostID)
			if !r.matchesHost(hostID, tags, groupIDs) {
				continue
			}
			dedup := fmt.Sprintf("inventory_drift:%s:nic:%s", hostID, n)
			subj := fmt.Sprintf("[MonSys] new NIC %s on %s", n, hostID)
			body := fmt.Sprintf("New NIC %q appeared on host %s.", n, hostID)
			e.fire(ctx, r, subj, body, dedup)
		}
		e.invNics[hostID] = set
	}
}

func (e *Engine) driftMACChanged(ctx context.Context, r ruleRow) {
	rows, err := e.Pool.Query(ctx, `SELECT host_id, name, COALESCE(mac, '') FROM nics`)
	if err != nil {
		slog.Warn("alerts: inventory_drift mac query", "err", err)
		return
	}
	defer rows.Close()
	current := map[uuid.UUID]map[string]string{}
	for rows.Next() {
		var hostID uuid.UUID
		var n, mac string
		if err := rows.Scan(&hostID, &n, &mac); err != nil {
			continue
		}
		if current[hostID] == nil {
			current[hostID] = map[string]string{}
		}
		current[hostID][n] = mac
	}
	e.stateMu.Lock()
	defer e.stateMu.Unlock()
	for hostID, set := range current {
		prev, seen := e.invMacs[hostID]
		if !seen {
			e.invMacs[hostID] = set
			continue
		}
		for n, mac := range set {
			oldMac, ok := prev[n]
			if !ok || mac == oldMac || mac == "" || oldMac == "" {
				continue
			}
			tags, groupIDs := e.fetchHostScope(ctx, hostID)
			if !r.matchesHost(hostID, tags, groupIDs) {
				continue
			}
			dedup := fmt.Sprintf("inventory_drift:%s:mac:%s", hostID, n)
			subj := fmt.Sprintf("[MonSys] NIC %s MAC changed on %s", n, hostID)
			body := fmt.Sprintf("NIC %s on host %s changed MAC %s → %s.", n, hostID, oldMac, mac)
			e.fire(ctx, r, subj, body, dedup)
		}
		e.invMacs[hostID] = set
	}
}

// driftScalarHost handles single-value host attributes (kernel, distro).
// Caller passes the apparent cache map; we keep a single source of truth in
// the engine struct so concurrent ticks don't race.
//
// `column`: compile-time constant. driftScalarHost is wired today with two
// hard-coded values ("kernel" and "distro") from evalInventoryDrift's switch.
// A future patch that pipes operator-supplied column names into this helper
// would be SQL injection. See the SQL-injection contract above.
func (e *Engine) driftScalarHost(ctx context.Context, r ruleRow, column string, cache map[uuid.UUID]string) {
	rows, err := e.Pool.Query(ctx, fmt.Sprintf(`SELECT id, COALESCE(%s, '') FROM hosts`, column))
	if err != nil {
		slog.Warn("alerts: inventory_drift scalar query", "column", column, "err", err)
		return
	}
	defer rows.Close()
	current := map[uuid.UUID]string{}
	for rows.Next() {
		var hostID uuid.UUID
		var v string
		if err := rows.Scan(&hostID, &v); err != nil {
			continue
		}
		current[hostID] = v
	}
	e.stateMu.Lock()
	defer e.stateMu.Unlock()
	for hostID, v := range current {
		prev, seen := cache[hostID]
		if !seen || prev == "" {
			cache[hostID] = v
			continue
		}
		if prev == v || v == "" {
			continue
		}
		tags, groupIDs := e.fetchHostScope(ctx, hostID)
		if !r.matchesHost(hostID, tags, groupIDs) {
			continue
		}
		dedup := fmt.Sprintf("inventory_drift:%s:%s", hostID, column)
		subj := fmt.Sprintf("[MonSys] %s changed on %s: %s → %s", column, hostID, prev, v)
		body := fmt.Sprintf("Host %s %s changed from %q to %q.", hostID, column, prev, v)
		e.fire(ctx, r, subj, body, dedup)
		cache[hostID] = v
	}
}

// --- login_anomaly --------------------------------------------------------
func (e *Engine) evalLoginAnomaly(ctx context.Context) {
	rules, err := e.loadRules(ctx, "login_anomaly")
	if err != nil || len(rules) == 0 {
		if err != nil {
			slog.Warn("alerts: load login_anomaly rules", "err", err)
		}
		return
	}
	for _, r := range rules {
		kind := stringParam(r.ConditionParams, "kind", "")
		// F-5: clamp window to [1 s, 24 h].
		windowSec := clampInt(intParam(r.ConditionParams, "window_sec", 86400), 1, 86400)
		threshold := intParam(r.ConditionParams, "threshold", 1)
		switch kind {
		case "new_source_ip":
			e.loginAnomalyNewIP(ctx, r, windowSec)
		case "root_success":
			e.loginAnomalyRoot(ctx, r, windowSec, threshold)
		case "sudo_spike":
			e.loginAnomalySudo(ctx, r, windowSec, threshold)
		default:
			slog.Warn("alerts: login_anomaly unsupported kind", "rule", r.Name, "kind", kind)
		}
	}
}

func (e *Engine) loginAnomalyNewIP(ctx context.Context, r ruleRow, windowSec int) {
	// A "new" source_ip is one observed in the recent window that we have
	// not previously seen for this (host, username) combination. We keep an
	// in-memory seen-set so an existing IP from before the engine started
	// doesn't fire after restart — first tick seeds the set.
	//
	// F-6: the seen-set is bounded at loginNewIPSeenMax entries. When the
	// cap is reached the oldest entry (FIFO insertion order) is evicted.
	// Eviction means the engine will eventually re-flag a stale IP as
	// "new" — acceptable given the 100k-entry ceiling represents many
	// months of operator-visible source IPs in realistic deployments.
	//
	// F-20: the "have we seen ANY IP for this (host, user)?" check used
	// to scan every key in loginNewIPSeen looking for a matching prefix —
	// O(n) per login event. Replaced by loginUserSeen, a per-(host, user)
	// first-seen-IP map. Constant-time lookup.
	rows, err := e.Pool.Query(ctx, `
		SELECT host_id, COALESCE(username, ''), COALESCE(source_ip, '')
		FROM login_events
		WHERE success = TRUE
		  AND time > now() - make_interval(secs => $1)
		  AND source_ip IS NOT NULL AND source_ip <> ''`, windowSec)
	if err != nil {
		slog.Warn("alerts: login_anomaly new_source_ip query", "err", err)
		return
	}
	defer rows.Close()
	for rows.Next() {
		var hostID uuid.UUID
		var user, ip string
		if err := rows.Scan(&hostID, &user, &ip); err != nil {
			continue
		}
		key := hostID.String() + ":" + user + ":" + ip
		userKey := hostID.String() + ":" + user
		e.stateMu.Lock()
		seen := e.loginNewIPSeen[key]
		if !seen {
			// Insert with FIFO eviction. The size check before insertion
			// keeps the map at or below the cap.
			for len(e.loginNewIPSeen) >= loginNewIPSeenMax && len(e.loginNewIPOrder) > 0 {
				oldest := e.loginNewIPOrder[0]
				e.loginNewIPOrder = e.loginNewIPOrder[1:]
				delete(e.loginNewIPSeen, oldest)
			}
			e.loginNewIPSeen[key] = true
			e.loginNewIPOrder = append(e.loginNewIPOrder, key)
		}
		// Track first-seen IP for this (host, user) so the next branch
		// (engine-boot seed vs. genuine new IP) is O(1) instead of O(n).
		firstIP, hadUser := e.loginUserSeen[userKey]
		if !hadUser {
			e.loginUserSeen[userKey] = ip
		}
		e.stateMu.Unlock()
		if seen {
			continue
		}
		// F-20: replace the O(n) prefix scan with a constant-time check.
		// hadUser==true means we've already recorded another IP for this
		// (host, user); this is a genuine new IP. hadUser==false means
		// this row is the very first login for the user — treat as seed
		// (don't fire). firstIP being equal to ip is the dedup case where
		// the same IP got reinserted after eviction.
		if !hadUser || firstIP == ip {
			continue
		}
		tags, groupIDs := e.fetchHostScope(ctx, hostID)
		if !r.matchesHost(hostID, tags, groupIDs) {
			continue
		}
		dedup := fmt.Sprintf("login_anomaly:%s:new_ip:%s", hostID, ip)
		subj := fmt.Sprintf("[MonSys] new source IP %s for user %s on %s", ip, user, hostID)
		body := fmt.Sprintf("Successful login on host %s as %q from previously-unseen source IP %s.",
			hostID, user, ip)
		e.fire(ctx, r, subj, body, dedup)
	}
}

func (e *Engine) loginAnomalyRoot(ctx context.Context, r ruleRow, windowSec, threshold int) {
	rows, err := e.Pool.Query(ctx, `
		SELECT host_id, count(*)
		FROM login_events
		WHERE success = TRUE
		  AND username = 'root'
		  AND time > now() - make_interval(secs => $1)
		GROUP BY host_id
		HAVING count(*) >= $2`, windowSec, threshold)
	if err != nil {
		slog.Warn("alerts: login_anomaly root query", "err", err)
		return
	}
	defer rows.Close()
	for rows.Next() {
		var hostID uuid.UUID
		var n int
		if err := rows.Scan(&hostID, &n); err != nil {
			continue
		}
		tags, groupIDs := e.fetchHostScope(ctx, hostID)
		if !r.matchesHost(hostID, tags, groupIDs) {
			continue
		}
		dedup := fmt.Sprintf("login_anomaly:%s:root", hostID)
		subj := fmt.Sprintf("[MonSys] root login on %s (%d in %ds)", hostID, n, windowSec)
		body := fmt.Sprintf("Host %s observed %d successful root logins in the last %ds (threshold %d).",
			hostID, n, windowSec, threshold)
		e.fire(ctx, r, subj, body, dedup)
	}
}

func (e *Engine) loginAnomalySudo(ctx context.Context, r ruleRow, windowSec, threshold int) {
	rows, err := e.Pool.Query(ctx, `
		SELECT host_id, COALESCE(username, ''), count(*)
		FROM login_events
		WHERE method = 'sudo'
		  AND time > now() - make_interval(secs => $1)
		GROUP BY host_id, username
		HAVING count(*) > $2`, windowSec, threshold)
	if err != nil {
		slog.Warn("alerts: login_anomaly sudo query", "err", err)
		return
	}
	defer rows.Close()
	for rows.Next() {
		var hostID uuid.UUID
		var user string
		var n int
		if err := rows.Scan(&hostID, &user, &n); err != nil {
			continue
		}
		tags, groupIDs := e.fetchHostScope(ctx, hostID)
		if !r.matchesHost(hostID, tags, groupIDs) {
			continue
		}
		dedup := fmt.Sprintf("login_anomaly:%s:sudo:%s", hostID, user)
		subj := fmt.Sprintf("[MonSys] sudo spike for %s on %s (%d in %ds)", user, hostID, n, windowSec)
		body := fmt.Sprintf("User %s on host %s invoked sudo %d times in %ds (threshold %d).",
			user, hostID, n, windowSec, threshold)
		e.fire(ctx, r, subj, body, dedup)
	}
}

// --- audit_action ---------------------------------------------------------
//
// Audit_log has no host_id column. Dispatch is "fire on every new row that
// matches actions[] / actor_pattern / target_pattern" with no host scoping.
// We use a per-rule in-memory cursor (last seen `at`) so a rule fires only
// on rows added since the previous tick. The first tick after boot seeds the
// cursor at now() so historical rows don't replay.
//
// F-3 (audit_action regex DoS — defence in depth):
//
//  1. Patterns are length-capped (256) and Go-regexp validated at rule write
//     time in store.validateAuditActionParams, so catastrophic-backtracking
//     constructs are rejected before they reach the DB.
//
//  2. We additionally bind the audit query to a 2-second per-statement
//     timeout via SET LOCAL statement_timeout inside a transaction. If a
//     pattern that slipped past validation (e.g. the rule predates this
//     hardening) pins the Postgres POSIX regex engine, the backend frees
//     itself after 2 s instead of staying wedged.
//
// F-13 (audit action filter — safe by construction): `action = ANY($N)`
// with a []string parameter is parameterised through pgx — no concatenation,
// no SQL injection surface. Confirming here so a future refactor that
// switches to fmt.Sprintf("action IN (%s)", strings.Join(...)) gets caught
// in review.
func (e *Engine) evalAuditAction(ctx context.Context) {
	rules, err := e.loadRules(ctx, "audit_action")
	if err != nil || len(rules) == 0 {
		if err != nil {
			slog.Warn("alerts: load audit_action rules", "err", err)
		}
		return
	}
	for _, r := range rules {
		actions := stringSliceParam(r.ConditionParams, "actions", nil)
		actorPat := stringParam(r.ConditionParams, "actor_pattern", "")
		targetPat := stringParam(r.ConditionParams, "target_pattern", "")
		e.stateMu.Lock()
		cursor, ok := e.auditLastAt[r.ID]
		if !ok {
			e.auditLastAt[r.ID] = time.Now()
			e.stateMu.Unlock()
			continue
		}
		e.stateMu.Unlock()
		conds := []string{"at > $1"}
		args := []any{cursor}
		if len(actions) > 0 {
			// F-13: parameterised array predicate; pgx escapes for us.
			conds = append(conds, fmt.Sprintf("action = ANY($%d)", len(args)+1))
			args = append(args, actions)
		}
		if actorPat != "" {
			conds = append(conds, fmt.Sprintf("COALESCE(actor,'') ~ $%d", len(args)+1))
			args = append(args, actorPat)
		}
		if targetPat != "" {
			conds = append(conds, fmt.Sprintf("COALESCE(target,'') ~ $%d", len(args)+1))
			args = append(args, targetPat)
		}
		sql := "SELECT id, at, COALESCE(actor,''), action, COALESCE(target,'') FROM audit_log WHERE " +
			strings.Join(conds, " AND ") + " ORDER BY at ASC, id ASC"

		// F-3: run the audit scan inside a tx with a 2 s statement_timeout
		// so a slipped pathological regex can't pin a backend. tx is
		// read-only by construction (single SELECT) — rolled back at end
		// of scope.
		maxAt := cursor
		func() {
			tx, terr := e.Pool.BeginTx(ctx, pgx.TxOptions{AccessMode: pgx.ReadOnly})
			if terr != nil {
				slog.Warn("alerts: audit_action begin tx", "err", terr)
				return
			}
			defer func() { _ = tx.Rollback(ctx) }()
			if _, terr := tx.Exec(ctx, "SET LOCAL statement_timeout = '2s'"); terr != nil {
				slog.Warn("alerts: audit_action set statement_timeout", "err", terr)
				return
			}
			rows, qerr := tx.Query(ctx, sql, args...)
			if qerr != nil {
				slog.Warn("alerts: audit_action query", "err", qerr)
				return
			}
			defer rows.Close()
			for rows.Next() {
				var id int64
				var at time.Time
				var actor, action, target string
				if err := rows.Scan(&id, &at, &actor, &action, &target); err != nil {
					continue
				}
				if at.After(maxAt) {
					maxAt = at
				}
				dedup := fmt.Sprintf("audit_action:%s:%d", uuid.Nil, id)
				subj := fmt.Sprintf("[MonSys] audit: %s by %s", action, actor)
				body := fmt.Sprintf("Audit event id=%d at=%s actor=%q action=%q target=%q.",
					id, at.UTC().Format(time.RFC3339), actor, action, target)
				e.fire(ctx, r, subj, body, dedup)
			}
		}()
		e.stateMu.Lock()
		e.auditLastAt[r.ID] = maxAt
		e.stateMu.Unlock()
	}
}

// --- firewall_state_change ------------------------------------------------
func (e *Engine) evalFirewallStateChange(ctx context.Context) {
	rules, err := e.loadRules(ctx, "firewall_state_change")
	if err != nil || len(rules) == 0 {
		if err != nil {
			slog.Warn("alerts: load firewall_state_change rules", "err", err)
		}
		return
	}
	rows, err := e.Pool.Query(ctx, `
		SELECT host_id, engine, active, COALESCE(default_input, ''), COALESCE(rule_count, 0)
		FROM firewall_status`)
	if err != nil {
		slog.Warn("alerts: firewall_state_change query", "err", err)
		return
	}
	defer rows.Close()
	type fs struct {
		hostID         uuid.UUID
		engine, policy string
		active         bool
		rules          int
	}
	var snapshot []fs
	for rows.Next() {
		var f fs
		if err := rows.Scan(&f.hostID, &f.engine, &f.active, &f.policy, &f.rules); err != nil {
			continue
		}
		snapshot = append(snapshot, f)
	}
	for _, f := range snapshot {
		key := f.hostID.String() + ":" + f.engine
		e.stateMu.Lock()
		prevActive, sawActive := e.firewallActive[f.hostID.String()+":"+f.engine]
		prevPolicy, sawPolicy := e.firewallPolicy[key]
		prevRules, sawRules := e.firewallRules[key]
		e.firewallActive[key] = f.active
		e.firewallPolicy[key] = f.policy
		e.firewallRules[key] = f.rules
		e.stateMu.Unlock()
		for _, r := range rules {
			kind := stringParam(r.ConditionParams, "kind", "")
			tags, groupIDs := e.fetchHostScope(ctx, f.hostID)
			if !r.matchesHost(f.hostID, tags, groupIDs) {
				continue
			}
			switch kind {
			case "inactive":
				if sawActive && prevActive && !f.active {
					dedup := fmt.Sprintf("firewall_state_change:%s:%s:inactive", f.hostID, f.engine)
					subj := fmt.Sprintf("[MonSys] firewall %s went inactive on %s", f.engine, f.hostID)
					body := fmt.Sprintf("Firewall engine %s on host %s transitioned to inactive.", f.engine, f.hostID)
					e.fire(ctx, r, subj, body, dedup)
				}
			case "default_policy_weakened":
				if sawPolicy && policyIsRestrictive(prevPolicy) && !policyIsRestrictive(f.policy) {
					dedup := fmt.Sprintf("firewall_state_change:%s:%s:policy", f.hostID, f.engine)
					subj := fmt.Sprintf("[MonSys] firewall %s default policy weakened on %s", f.engine, f.hostID)
					body := fmt.Sprintf("Firewall %s default_input changed %s → %s.", f.engine, prevPolicy, f.policy)
					e.fire(ctx, r, subj, body, dedup)
				}
			case "rule_count_drop":
				dropT := intParam(r.ConditionParams, "drop_threshold", 5)
				if sawRules && prevRules-f.rules >= dropT {
					dedup := fmt.Sprintf("firewall_state_change:%s:%s:rules", f.hostID, f.engine)
					subj := fmt.Sprintf("[MonSys] firewall %s rule count dropped on %s", f.engine, f.hostID)
					body := fmt.Sprintf("Firewall %s rule_count dropped %d → %d (Δ %d, threshold %d).",
						f.engine, prevRules, f.rules, prevRules-f.rules, dropT)
					e.fire(ctx, r, subj, body, dedup)
				}
			default:
				// Unknown subkind — skip silently to avoid log spam every tick.
			}
		}
	}
}

// policyIsRestrictive reports whether a default-chain policy string indicates
// blocking (drop/deny/reject) vs. permissive (accept/allow/permit).
func policyIsRestrictive(p string) bool {
	switch strings.ToLower(strings.TrimSpace(p)) {
	case "drop", "deny", "reject":
		return true
	}
	return false
}

// --- fail2ban_jail_disappeared --------------------------------------------
//
// We track which (host, jail) pairs we've ever seen. On each tick we mark
// the ones still present; pairs that were previously seen but absent in the
// current snapshot fire a one-shot alert.
func (e *Engine) evalFail2banJailDisappeared(ctx context.Context) {
	rules, err := e.loadRules(ctx, "fail2ban_jail_disappeared")
	if err != nil || len(rules) == 0 {
		if err != nil {
			slog.Warn("alerts: load fail2ban_jail_disappeared rules", "err", err)
		}
		return
	}
	rows, err := e.Pool.Query(ctx, `SELECT host_id, jail FROM fail2ban_jails`)
	if err != nil {
		slog.Warn("alerts: fail2ban_jail_disappeared query", "err", err)
		return
	}
	defer rows.Close()
	current := map[string]map[string]bool{}
	for rows.Next() {
		var hostID uuid.UUID
		var jail string
		if err := rows.Scan(&hostID, &jail); err != nil {
			continue
		}
		hk := hostID.String()
		if current[hk] == nil {
			current[hk] = map[string]bool{}
		}
		current[hk][jail] = true
	}
	e.stateMu.Lock()
	prev := e.fail2banSeen
	// First-tick seed: capture but don't fire.
	if len(prev) == 0 {
		e.fail2banSeen = current
		e.stateMu.Unlock()
		return
	}
	e.stateMu.Unlock()
	for hk, jails := range prev {
		hostID, err := uuid.Parse(hk)
		if err != nil {
			continue
		}
		curJails := current[hk]
		for jail := range jails {
			if curJails[jail] {
				continue
			}
			for _, r := range rules {
				tags, groupIDs := e.fetchHostScope(ctx, hostID)
				if !r.matchesHost(hostID, tags, groupIDs) {
					continue
				}
				dedup := fmt.Sprintf("fail2ban_jail_disappeared:%s:%s", hostID, jail)
				subj := fmt.Sprintf("[MonSys] fail2ban jail %s disappeared on %s", jail, hostID)
				body := fmt.Sprintf("fail2ban jail %q on host %s is no longer reported.", jail, hostID)
				e.fire(ctx, r, subj, body, dedup)
			}
		}
	}
	// Replace baseline with the union — newly observed jails should now be
	// tracked too, but disappeared ones stay flagged on the next tick if
	// they came back (resolve isn't wired here since the table doesn't carry
	// a "removed_at" we can read).
	e.stateMu.Lock()
	for hk, jails := range current {
		if e.fail2banSeen[hk] == nil {
			e.fail2banSeen[hk] = map[string]bool{}
		}
		for j := range jails {
			e.fail2banSeen[hk][j] = true
		}
	}
	e.stateMu.Unlock()
}

// --- crowdsec_decision_threshold ------------------------------------------
func (e *Engine) evalCrowdSecDecisionThreshold(ctx context.Context) {
	rules, err := e.loadRules(ctx, "crowdsec_decision_threshold")
	if err != nil || len(rules) == 0 {
		if err != nil {
			slog.Warn("alerts: load crowdsec_decision_threshold rules", "err", err)
		}
		return
	}
	for _, r := range rules {
		threshold := intParam(r.ConditionParams, "threshold", 100)
		rows, err := e.Pool.Query(ctx, `
			SELECT host_id, count(*)
			FROM crowdsec_decisions
			WHERE until > now()
			GROUP BY host_id
			HAVING count(*) > $1`, threshold)
		if err != nil {
			slog.Warn("alerts: crowdsec_decision_threshold query", "err", err)
			continue
		}
		for rows.Next() {
			var hostID uuid.UUID
			var n int
			if err := rows.Scan(&hostID, &n); err != nil {
				continue
			}
			tags, groupIDs := e.fetchHostScope(ctx, hostID)
			if !r.matchesHost(hostID, tags, groupIDs) {
				continue
			}
			dedup := fmt.Sprintf("crowdsec_decisions:%s", hostID)
			subj := fmt.Sprintf("[MonSys] crowdsec decisions on %s: %d > %d", hostID, n, threshold)
			body := fmt.Sprintf("Host %s has %d active CrowdSec decisions (threshold %d).",
				hostID, n, threshold)
			e.fire(ctx, r, subj, body, dedup)
		}
		rows.Close()
	}
}
