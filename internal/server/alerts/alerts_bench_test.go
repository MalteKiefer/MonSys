package alerts

import (
	"fmt"
	"testing"

	"github.com/google/uuid"

	"github.com/MalteKiefer/MonSys/internal/server/probe"
)

// BenchmarkSplitDedupKey covers the dedup-key parser called by fire() for
// every alert that makes it past throttling. The hot path is the
// strings.IndexByte + uuid.Parse + the switch over the 25-ish known
// kinds, all CPU-bound with no DB.
func BenchmarkSplitDedupKey(b *testing.B) {
	cases := []struct {
		name  string
		dedup string
	}{
		{"host_offline", "host_offline:550e8400-e29b-41d4-a716-446655440000"},
		{"monitor_failed", "monitor_failed:550e8400-e29b-41d4-a716-446655440000"},
		{"metric_w_scope", "metric_threshold:550e8400-e29b-41d4-a716-446655440000:/var/log"},
		{"unknown_kind", "made_up_kind:550e8400-e29b-41d4-a716-446655440000"},
		{"malformed", "no-colon-here"},
	}
	for _, c := range cases {
		b.Run(c.name, func(b *testing.B) {
			b.ReportAllocs()
			for i := 0; i < b.N; i++ {
				_, _ = splitDedupKey(c.dedup)
			}
		})
	}
}

// BenchmarkParseChannelUUIDs measures the allocation cost of the
// per-fire channel-list parse. Each fire() builds a []uuid.UUID from the
// rule's stored channel id strings. With many channels per rule this
// becomes the dominant per-fire alloc cost.
func BenchmarkParseChannelUUIDs(b *testing.B) {
	cases := []int{1, 5, 20}
	for _, n := range cases {
		in := make([]string, n)
		for i := range in {
			in[i] = uuid.New().String()
		}
		b.Run(fmt.Sprintf("n=%d", n), func(b *testing.B) {
			b.ReportAllocs()
			for i := 0; i < b.N; i++ {
				_ = parseChannelUUIDs(in)
			}
		})
	}
}

// BenchmarkRuleMatchesHost stresses the per-(rule, host) targeting
// predicate. For periodic evaluators (security_updates_pending,
// login_failed_threshold, metric_threshold) we invoke matchesHost once
// per (rule × candidate host) pair, so this scales O(R × H) per tick.
func BenchmarkRuleMatchesHost(b *testing.B) {
	hostID := uuid.New()
	otherHosts := make([]uuid.UUID, 99)
	for i := range otherHosts {
		otherHosts[i] = uuid.New()
	}
	groupID := uuid.New()
	groupIDs := []uuid.UUID{groupID}
	tags := []string{"prod", "web", "tier-1"}

	cases := []struct {
		name string
		r    ruleRow
	}{
		{
			name: "wildcard",
			r:    ruleRow{},
		},
		{
			name: "host-direct-hit",
			r:    ruleRow{TargetHostIDs: append([]uuid.UUID{hostID}, otherHosts...)},
		},
		{
			name: "host-miss",
			r:    ruleRow{TargetHostIDs: otherHosts},
		},
		{
			name: "tag-hit",
			r:    ruleRow{TargetTags: []string{"web"}},
		},
		{
			name: "group-hit",
			r:    ruleRow{TargetGroupIDs: groupIDs},
		},
	}
	for _, c := range cases {
		b.Run(c.name, func(b *testing.B) {
			b.ReportAllocs()
			for i := 0; i < b.N; i++ {
				_ = c.r.matchesHost(hostID, tags, groupIDs)
			}
		})
	}
}

// BenchmarkRuleMatchesMonitor covers the monitor-name/type filter the
// engine runs against every monitor_failed rule on every probe result
// event. monitor_failed dispatches are the high-frequency path; this is
// pure map[string]any lookup + string compare.
func BenchmarkRuleMatchesMonitor(b *testing.B) {
	ev := probe.ResultEvent{
		MonitorID: uuid.New(),
		Type:      "http",
		Name:      "auth-service",
	}
	cases := []struct {
		name   string
		params map[string]any
	}{
		{"no-filter", nil},
		{"type-filter-hit", map[string]any{"monitor_type": "http"}},
		{"type-filter-miss", map[string]any{"monitor_type": "tcp"}},
		{"name-filter-hit", map[string]any{"monitor_name": "auth-service"}},
		{"both-filters", map[string]any{"monitor_type": "http", "monitor_name": "auth-service"}},
	}
	for _, c := range cases {
		r := ruleRow{ConditionParams: c.params}
		if r.ConditionParams == nil {
			r.ConditionParams = map[string]any{}
		}
		b.Run(c.name, func(b *testing.B) {
			b.ReportAllocs()
			for i := 0; i < b.N; i++ {
				_ = ruleMatchesMonitor(r, ev)
			}
		})
	}
}

// BenchmarkParamHelpers covers the condition_params unpacking helpers
// invoked once per evaluator tick per rule. These produce no allocs in
// the common case (numeric and bool params) but mapParam allocates a
// new map[string]string for every call — that allocation cost is what
// this benchmark surfaces.
func BenchmarkParamHelpers(b *testing.B) {
	m := map[string]any{
		"metric":     "cpu_usage_pct",
		"comparator": ">",
		"value":      80.0,
		"window_sec": 300.0,
		"for_sec":    60.0,
		"enabled":    true,
		"scope":      map[string]any{"mountpoint": "/var", "nic": "eno1"},
	}
	b.Run("intParam", func(b *testing.B) {
		b.ReportAllocs()
		for i := 0; i < b.N; i++ {
			_ = intParam(m, "window_sec", 0)
		}
	})
	b.Run("floatParam", func(b *testing.B) {
		b.ReportAllocs()
		for i := 0; i < b.N; i++ {
			_ = floatParam(m, "value", 0)
		}
	})
	b.Run("stringParam", func(b *testing.B) {
		b.ReportAllocs()
		for i := 0; i < b.N; i++ {
			_ = stringParam(m, "metric", "")
		}
	})
	b.Run("mapParam", func(b *testing.B) {
		b.ReportAllocs()
		for i := 0; i < b.N; i++ {
			_ = mapParam(m, "scope")
		}
	})
	b.Run("sqlComparator", func(b *testing.B) {
		b.ReportAllocs()
		for i := 0; i < b.N; i++ {
			_ = sqlComparator(">=")
		}
	})
}

// BenchmarkDispatchMetricThreshold measures the per-dispatch in-memory
// cost on the dispatch hot path: build a fresh ruleRow, parse the dedup
// key, parse channel UUIDs. We can't run runMetricRule end-to-end without
// a live DB, but those three ops are the entire CPU+alloc cost the
// dispatch path adds on top of the (DB-blocked) SQL window query.
func BenchmarkDispatchMetricThreshold(b *testing.B) {
	channels := []string{
		uuid.New().String(),
		uuid.New().String(),
		uuid.New().String(),
	}
	hostID := uuid.New()
	dedup := fmt.Sprintf("metric_threshold:%s:/var", hostID)
	r := ruleRow{
		ID:            uuid.New(),
		Name:          "high-cpu",
		ConditionType: "metric_threshold",
		ChannelIDs:    channels,
		Severity:      "warning",
		ConditionParams: map[string]any{
			"metric":     "cpu_usage_pct",
			"comparator": ">",
			"value":      80.0,
			"window_sec": 300.0,
		},
	}
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		_, _ = splitDedupKey(dedup)
		_ = parseChannelUUIDs(r.ChannelIDs)
		_ = sqlComparator(r.ConditionParams["comparator"].(string))
		_ = floatParam(r.ConditionParams, "value", 0)
		_ = intParam(r.ConditionParams, "window_sec", 120)
	}
}
