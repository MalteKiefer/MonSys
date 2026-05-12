package alerts

import (
	"testing"

	"github.com/MalteKiefer/MonSys/internal/server/probe"
)

// TestClampInt walks the three branches of clampInt so a future tweak to the
// inclusive boundaries trips immediately.
func TestClampInt(t *testing.T) {
	cases := []struct {
		name             string
		v, lo, hi, wantR int
	}{
		{"in range", 5, 1, 10, 5},
		{"at low bound", 1, 1, 10, 1},
		{"at high bound", 10, 1, 10, 10},
		{"below low", -1, 1, 10, 1},
		{"above high", 99, 1, 10, 10},
		{"window_sec sane", 7200, 1, 86400, 7200},
		{"window_sec runaway", 31536000, 1, 86400, 86400},
		{"window_sec negative", -10, 1, 86400, 1},
	}
	for _, c := range cases {
		got := clampInt(c.v, c.lo, c.hi)
		if got != c.wantR {
			t.Errorf("[%s] clampInt(%d,%d,%d) = %d, want %d", c.name, c.v, c.lo, c.hi, got, c.wantR)
		}
	}
}

// TestSqlComparator pins the SQL-safe allow-list. Any return not in this set
// would be an injection sink for metric_threshold rules.
func TestSqlComparator(t *testing.T) {
	cases := []struct {
		in, want string
	}{
		{">", ">"},
		{">=", ">="},
		{"<", "<"},
		{"<=", "<="},
		{"=", ""},
		{"==", ""},
		{"!=", ""},
		{"; DROP TABLE hosts; --", ""},
		{"", ""},
		{"> OR 1=1", ""},
	}
	for _, c := range cases {
		got := sqlComparator(c.in)
		if got != c.want {
			t.Errorf("sqlComparator(%q) = %q, want %q", c.in, got, c.want)
		}
	}
}

// TestIntParam covers the float64/int branches plus the missing-key default,
// matching the shapes Postgres' JSONB decoder produces.
func TestIntParam(t *testing.T) {
	m := map[string]any{
		"as_float": float64(120),
		"as_int":   int(45),
		"as_str":   "120",
		"nil":      nil,
	}
	cases := []struct {
		key  string
		def  int
		want int
	}{
		{"as_float", 1, 120},
		{"as_int", 1, 45},
		{"as_str", 99, 99}, // wrong type falls back to default
		{"nil", 7, 7},
		{"missing", 42, 42},
	}
	for _, c := range cases {
		got := intParam(m, c.key, c.def)
		if got != c.want {
			t.Errorf("intParam(%q, def=%d) = %d, want %d", c.key, c.def, got, c.want)
		}
	}
}

// TestFloatParam covers the float-friendly numeric paths.
func TestFloatParam(t *testing.T) {
	m := map[string]any{
		"f":   float64(80.5),
		"i":   int(42),
		"str": "80.5",
	}
	cases := []struct {
		key  string
		def  float64
		want float64
	}{
		{"f", 0, 80.5},
		{"i", 0, 42},
		{"str", 1.5, 1.5},
		{"missing", 99.9, 99.9},
	}
	for _, c := range cases {
		got := floatParam(m, c.key, c.def)
		if got != c.want {
			t.Errorf("floatParam(%q, def=%v) = %v, want %v", c.key, c.def, got, c.want)
		}
	}
}

// TestStringParam refuses the empty string (treats it as missing) and falls
// back to the caller-supplied default for type mismatches.
func TestStringParam(t *testing.T) {
	m := map[string]any{
		"name":    "cpu_usage_pct",
		"empty":   "",
		"numeric": float64(7),
	}
	cases := []struct {
		key, def, want string
	}{
		{"name", "default", "cpu_usage_pct"},
		{"empty", "default", "default"},
		{"numeric", "default", "default"},
		{"missing", "fallback", "fallback"},
	}
	for _, c := range cases {
		got := stringParam(m, c.key, c.def)
		if got != c.want {
			t.Errorf("stringParam(%q, def=%q) = %q, want %q", c.key, c.def, got, c.want)
		}
	}
}

// TestBoolParam pins the simple-typed branch.
func TestBoolParam(t *testing.T) {
	m := map[string]any{"t": true, "f": false, "x": "true"}
	if !boolParam(m, "t", false) {
		t.Error("expected true")
	}
	if boolParam(m, "f", true) {
		t.Error("expected false")
	}
	if !boolParam(m, "x", true) {
		t.Error("default returned for wrong-typed entry")
	}
	if !boolParam(m, "missing", true) {
		t.Error("default returned for missing key")
	}
}

// TestMapParam unpacks a nested map[string]any (the shape pgx hands back for
// jsonb objects) and returns a friendly string→string map.
func TestMapParam(t *testing.T) {
	cases := []struct {
		name string
		in   map[string]any
		want map[string]string
	}{
		{
			"nested scope",
			map[string]any{"scope": map[string]any{"mountpoint": "/var", "nic": "eth0"}},
			map[string]string{"mountpoint": "/var", "nic": "eth0"},
		},
		{"missing", map[string]any{}, map[string]string{}},
		{"wrong type", map[string]any{"scope": "not an object"}, map[string]string{}},
		{
			"mixed values",
			map[string]any{"scope": map[string]any{"k": "v", "n": float64(1)}},
			map[string]string{"k": "v"},
		},
	}
	for _, c := range cases {
		got := mapParam(c.in, "scope")
		if len(got) != len(c.want) {
			t.Fatalf("[%s] got %d keys, want %d (got=%v)", c.name, len(got), len(c.want), got)
		}
		for k, v := range c.want {
			if got[k] != v {
				t.Errorf("[%s] key %q: got %q, want %q", c.name, k, got[k], v)
			}
		}
	}
}

// TestStringSliceParam covers all three branches: native []string, jsonb
// []any, and the missing-key default.
func TestStringSliceParam(t *testing.T) {
	def := []string{"d"}
	cases := []struct {
		name string
		in   map[string]any
		want []string
	}{
		{"native []string", map[string]any{"tags": []string{"a", "b"}}, []string{"a", "b"}},
		{"jsonb []any", map[string]any{"tags": []any{"a", "b"}}, []string{"a", "b"}},
		{"mixed types", map[string]any{"tags": []any{"a", 1, "b"}}, []string{"a", "b"}},
		{"empty []any", map[string]any{"tags": []any{}}, def},
		{"missing", map[string]any{}, def},
	}
	for _, c := range cases {
		got := stringSliceParam(c.in, "tags", def)
		if len(got) != len(c.want) {
			t.Fatalf("[%s] got %v, want %v", c.name, got, c.want)
		}
		for i := range got {
			if got[i] != c.want[i] {
				t.Errorf("[%s] idx %d: got %q, want %q", c.name, i, got[i], c.want[i])
			}
		}
	}
}

// TestContains is a small case-insensitive helper used by several
// evaluators; lock down the case-fold behaviour.
func TestContains(t *testing.T) {
	if !contains([]string{"alpha", "Beta"}, "BETA") {
		t.Error("expected case-insensitive match")
	}
	if contains([]string{"alpha"}, "gamma") {
		t.Error("did not expect match")
	}
	if contains(nil, "x") {
		t.Error("nil slice should not match")
	}
}

// TestTruncate clips strings to n bytes — used in audit-log composition so
// log rows never exceed the column limit.
func TestTruncate(t *testing.T) {
	if got := truncate("hello", 3); got != "hel" {
		t.Errorf("got %q, want %q", got, "hel")
	}
	if got := truncate("hi", 5); got != "hi" {
		t.Errorf("short string mangled: %q", got)
	}
	if got := truncate("", 10); got != "" {
		t.Errorf("empty got %q", got)
	}
}

// TestParseHHMM accepts "HH:MM" forms and rejects anything else. The
// helper allows 4- or 5-char strings (so "9:30" is fine) but rejects
// out-of-range hour/minute values and anything that fails atoi.
func TestParseHHMM(t *testing.T) {
	cases := []struct {
		in       string
		wantMins int
		wantOK   bool
	}{
		{"00:00", 0, true},
		{"23:59", 23*60 + 59, true},
		{"09:30", 9*60 + 30, true},
		{"24:00", 0, false},
		{"12:60", 0, false},
		{"abc", 0, false},
		{"", 0, false},
		{"123:45", 0, false}, // too long
		{"1:2", 0, false},    // too short
	}
	for _, c := range cases {
		got, ok := parseHHMM(c.in)
		if ok != c.wantOK {
			t.Errorf("parseHHMM(%q) ok=%v, want %v", c.in, ok, c.wantOK)
		}
		if ok && got != c.wantMins {
			t.Errorf("parseHHMM(%q) = %d mins, want %d", c.in, got, c.wantMins)
		}
	}
}

// TestPolicyIsRestrictive locks in the firewall-policy classification used
// by the firewall-state-change evaluator. New default-policy strings must
// be added explicitly — silent passthrough would mute the alert.
func TestPolicyIsRestrictive(t *testing.T) {
	for _, p := range []string{"drop", "DROP", "deny", "Reject", " reject "} {
		if !policyIsRestrictive(p) {
			t.Errorf("policyIsRestrictive(%q) = false, want true", p)
		}
	}
	for _, p := range []string{"accept", "allow", "permit", "", "unknown"} {
		if policyIsRestrictive(p) {
			t.Errorf("policyIsRestrictive(%q) = true, want false", p)
		}
	}
}

// TestRuleMatchesMonitor exercises the filter applied before per-monitor
// dispatch. Empty filter keys match anything; non-empty must match exactly.
func TestRuleMatchesMonitor(t *testing.T) {
	ev := probe.ResultEvent{Type: "https", Name: "api-prod"}

	cases := []struct {
		name   string
		params map[string]any
		want   bool
	}{
		{"no filters", map[string]any{}, true},
		{"matching type", map[string]any{"monitor_type": "https"}, true},
		{"non-matching type", map[string]any{"monitor_type": "tcp"}, false},
		{"matching name", map[string]any{"monitor_name": "api-prod"}, true},
		{"non-matching name", map[string]any{"monitor_name": "api-staging"}, false},
		{"empty filter ignored", map[string]any{"monitor_type": "", "monitor_name": ""}, true},
		{"both match", map[string]any{"monitor_type": "https", "monitor_name": "api-prod"}, true},
	}
	for _, c := range cases {
		r := ruleRow{ConditionParams: c.params}
		got := ruleMatchesMonitor(r, ev)
		if got != c.want {
			t.Errorf("[%s] ruleMatchesMonitor = %v, want %v", c.name, got, c.want)
		}
	}
}

// TestParseChannelUUIDs filters non-UUID strings; the alert engine refuses
// to call sendChannel on a malformed id rather than letting it surface a
// pgx error mid-fan-out.
func TestParseChannelUUIDs(t *testing.T) {
	in := []string{
		"00000000-0000-0000-0000-000000000001",
		"bad-uuid",
		"00000000-0000-0000-0000-000000000002",
		"",
	}
	got := parseChannelUUIDs(in)
	if len(got) != 2 {
		t.Fatalf("got %d UUIDs, want 2 (input=%v)", len(got), in)
	}
}

// TestSplitDedupKey covers each of the three classes the function knows
// about: host-scoped kinds, monitor-scoped kinds, and unknown kinds (where
// the function must return (nil, nil) rather than panic). The helper
// strips anything after a second ':' (mountpoint / NIC / workload scope),
// so a "kind:host:extra" key produces the same (host, nil) tuple as
// "kind:host" alone.
func TestSplitDedupKey(t *testing.T) {
	hostUUID := "00000000-0000-0000-0000-000000000abc"
	monUUID := "00000000-0000-0000-0000-000000000def"

	// Host-scoped kind: returns (host, nil).
	host, mon := splitDedupKey("host_offline:" + hostUUID)
	if host == nil {
		t.Errorf("expected non-nil host arg for host-scoped key")
	}
	if mon != nil {
		t.Errorf("expected nil monitor arg for host-scoped key, got %v", mon)
	}

	// Same kind with an extra scope segment — second colon ignored.
	host2, mon2 := splitDedupKey("metric_threshold:" + hostUUID + ":/var")
	if host2 == nil {
		t.Errorf("expected non-nil host arg for scoped key")
	}
	if mon2 != nil {
		t.Errorf("expected nil monitor arg for scoped key")
	}

	// Monitor-scoped kind: returns (nil, monitor).
	host3, mon3 := splitDedupKey("monitor_failed:" + monUUID)
	if host3 != nil {
		t.Errorf("expected nil host arg for monitor-scoped key")
	}
	if mon3 == nil {
		t.Errorf("expected non-nil monitor arg for monitor-scoped key")
	}

	// Unknown kind: both nil, no panic.
	host4, mon4 := splitDedupKey("not_a_real_kind:" + hostUUID)
	if host4 != nil || mon4 != nil {
		t.Errorf("expected (nil, nil) for unknown kind, got (%v, %v)", host4, mon4)
	}

	// Malformed (no colon): both nil.
	host5, mon5 := splitDedupKey("nocolon")
	if host5 != nil || mon5 != nil {
		t.Errorf("expected (nil, nil) for malformed key, got (%v, %v)", host5, mon5)
	}
}

// TestFmtDurationSecs returns a human-friendly duration string for alert
// bodies. The exact wording is operator-visible; lock it down so a refactor
// doesn't change the rendered message.
func TestFmtDurationSecs(t *testing.T) {
	cases := []struct {
		in   int
		want string
	}{
		{0, "0s"},
		{45, "45s"},
		{60, "1m00s"},
		{125, "2m05s"},
		{3600, "1h00m"},
		{3661, "1h01m"},
	}
	for _, c := range cases {
		got := fmtDurationSecs(c.in)
		if got != c.want {
			t.Errorf("fmtDurationSecs(%d) = %q, want %q", c.in, got, c.want)
		}
	}
}
