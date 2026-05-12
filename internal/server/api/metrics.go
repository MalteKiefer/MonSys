// Package api domain metrics.
//
// Why these live here and not in telemetry:
//
//   - they reference business-level events (ingest, login, alert fire) that
//     only the api/alerts/store layers know about;
//   - keeping the registration close to the call sites makes it obvious
//     where a counter is incremented and what label values it accepts.
//
// All metrics are registered on telemetry.PromRegistry so the single
// /metrics endpoint (admin-gated, see api.go) surfaces them alongside the
// stdlib runtime metrics.

package api

import (
	"github.com/prometheus/client_golang/prometheus"

	"github.com/MalteKiefer/MonSys/internal/server/alerts"
	"github.com/MalteKiefer/MonSys/internal/server/store"
	"github.com/MalteKiefer/MonSys/internal/server/telemetry"
)

var (
	// metricIngestRequestsTotal counts every accepted /v1/ingest call,
	// labelled by host_id so operators can spot a single chatty agent.
	//
	// Cardinality note: host_id is bounded by the fleet size (the audit
	// caps a deployment at a few thousand hosts). Adding agent_version
	// would explode cardinality and add little value — the version is
	// already in the ingest payload and the admin UI surfaces it.
	metricIngestRequestsTotal = prometheus.NewCounterVec(prometheus.CounterOpts{
		Name: "mon_ingest_requests_total",
		Help: "Total ingest requests accepted from agents, by host_id.",
	}, []string{"host_id"})

	// metricAlertsFiredTotal counts every alerts.Engine.fire() that made it
	// past the dedup/throttle window. Labels:
	//   condition_type: host_offline, monitor_failed, cert_expiring, ...
	//   severity:       info, warning, critical
	// Both label sets are bounded enums so cardinality stays low.
	metricAlertsFiredTotal = prometheus.NewCounterVec(prometheus.CounterOpts{
		Name: "mon_alerts_fired_total",
		Help: "Total alert notifications fired by the alerts engine.",
	}, []string{"condition_type", "severity"})

	// metricSessionIssuedTotal counts successful Store.IssueSession() calls.
	// No label because there's nothing to slice — the metric simply tells
	// you the login throughput.
	metricSessionIssuedTotal = prometheus.NewCounter(prometheus.CounterOpts{
		Name: "mon_session_issued_total",
		Help: "Total user sessions issued (post-password and post-2FA combined).",
	})

	// metricLoginFailuresTotal counts every AuthenticateUser rejection.
	// Labels:
	//   reason: not_found | bad_password | locked_out | disabled | error
	// Bounded enum keeps cardinality tiny.
	metricLoginFailuresTotal = prometheus.NewCounterVec(prometheus.CounterOpts{
		Name: "mon_login_failures_total",
		Help: "Total failed login attempts at /v1/auth/login, by reason.",
	}, []string{"reason"})

	// metricDBQueryDuration captures pgx query latency. Wired through
	// otelpgx's tracing hook; the prom histogram is fed from inside the
	// store layer if/when we add explicit observation calls. For now the
	// metric is registered so it shows up in /metrics with zero samples;
	// the otelpgx-tracer surfaces per-query spans via OTLP.
	//
	// Buckets cover the 50us..2s range that 99% of OLTP-shaped queries
	// fall into; the open-ended Inf bucket catches the rare migration or
	// audit-chain scan.
	metricDBQueryDuration = prometheus.NewHistogramVec(prometheus.HistogramOpts{
		Name:    "mon_db_query_duration_seconds",
		Help:    "Postgres query duration, observed by the otelpgx tracer.",
		Buckets: []float64{0.0005, 0.001, 0.005, 0.01, 0.025, 0.05, 0.1, 0.25, 0.5, 1, 2, 5},
	}, []string{"op"})
)

func init() {
	telemetry.PromRegistry.MustRegister(
		metricIngestRequestsTotal,
		metricAlertsFiredTotal,
		metricSessionIssuedTotal,
		metricLoginFailuresTotal,
		metricDBQueryDuration,
	)

	// Wire the package-level hooks the alerts/store layers use to avoid a
	// circular import on this package. The hook variables are no-ops by
	// default; api/metrics.go is the only writer.
	alerts.OnAlertFired = MetricAlertFired
	store.OnSessionIssued = MetricSessionIssued
	store.OnLoginFailure = MetricLoginFailure
}

// MetricIngestAccepted increments the per-host ingest counter. Exported for
// the alerts and store packages even though the call site is currently
// inside api/api.go's handleIngest — the alternative was an awkward
// circular import via an "internal metrics hub" package.
func MetricIngestAccepted(hostID string) {
	metricIngestRequestsTotal.WithLabelValues(hostID).Inc()
}

// MetricAlertFired is called from alerts.Engine.fire() after the dedup +
// quiet-hours filters have already let the alert through. condition_type
// is the rule's condition_type column; severity is the resolved severity
// (with the "" -> "warning" fallback already applied by the caller).
func MetricAlertFired(conditionType, severity string) {
	metricAlertsFiredTotal.WithLabelValues(conditionType, severity).Inc()
}

// MetricSessionIssued bumps the session counter once per successful
// IssueSession call.
func MetricSessionIssued() { metricSessionIssuedTotal.Inc() }

// MetricLoginFailure increments the login-failure counter with a bounded
// reason label. Callers in handleLogin map AuthenticateUser errors to one
// of: not_found | bad_password | locked_out | disabled | error.
func MetricLoginFailure(reason string) {
	metricLoginFailuresTotal.WithLabelValues(reason).Inc()
}
