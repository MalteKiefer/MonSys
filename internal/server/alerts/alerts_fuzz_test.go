package alerts

import (
	"encoding/json"
	"strings"
	"testing"
)

// FuzzSQLComparator exercises sqlComparator with arbitrary comparator strings.
// This is the SQL-injection chokepoint for metric_threshold rules: anything
// that round-trips through this function ends up in a SQL fragment, so the
// allow-list must be airtight. Contract: only the four canonical comparators
// pass through; everything else returns "".
func FuzzSQLComparator(f *testing.F) {
	f.Add(">")
	f.Add(">=")
	f.Add("<")
	f.Add("<=")
	f.Add("=")
	f.Add("==")
	f.Add("; DROP TABLE hosts; --")
	f.Add("> OR 1=1")
	f.Add("")

	allowed := map[string]bool{">": true, ">=": true, "<": true, "<=": true}

	f.Fuzz(func(t *testing.T, cmp string) {
		got := sqlComparator(cmp)
		if got == "" {
			if allowed[cmp] {
				t.Fatalf("sqlComparator(%q) returned empty for an allowed comparator", cmp)
			}
			return
		}
		// Non-empty result must be one of the four allow-listed operators —
		// any other return would let attacker-controlled text into SQL.
		if !allowed[got] {
			t.Fatalf("sqlComparator(%q) = %q, not in allow-list", cmp, got)
		}
		// And it must be exactly what we whitelisted, never substring-matched
		// or extended (e.g. "> OR 1=1" must not return "> OR 1=1").
		if got != cmp {
			t.Fatalf("sqlComparator(%q) = %q, must be exact match when non-empty", cmp, got)
		}
		// Sanity: result must not contain whitespace or SQL meta chars.
		if strings.ContainsAny(got, " \t\n;'\"\\-") {
			t.Fatalf("sqlComparator(%q) = %q contains unsafe chars", cmp, got)
		}
	})
}

// FuzzParamHelpers exercises stringParam/intParam/floatParam/mapParam against
// arbitrary JSON blobs that get unmarshalled into map[string]any — the exact
// shape Postgres' JSONB decoder produces for condition_params. Contract: no
// panics on adversarial JSON; defaults are returned for missing/wrong-typed
// keys; UTF-8 validity is preserved.
func FuzzParamHelpers(f *testing.F) {
	f.Add([]byte(`{"metric":"cpu_usage_pct","comparator":">","value":80,"window_sec":120}`))
	f.Add([]byte(`{"scope":{"mountpoint":"/var"}}`))
	f.Add([]byte(`{"metric":null,"value":"not a number"}`))
	f.Add([]byte(`{}`))
	f.Add([]byte(`{"comparator":"; DROP TABLE hosts; --"}`))
	f.Add([]byte(`{"scope":[1,2,3]}`))
	f.Add([]byte(`{"value":1e308}`))

	f.Fuzz(func(t *testing.T, data []byte) {
		var m map[string]any
		if err := json.Unmarshal(data, &m); err != nil {
			return
		}
		// Must not panic on any of these calls regardless of map content.
		s := stringParam(m, "metric", "default-metric")
		c := stringParam(m, "comparator", ">")
		_ = intParam(m, "window_sec", 120)
		_ = intParam(m, "for_sec", 60)
		_ = floatParam(m, "value", 0)
		_ = boolParam(m, "enabled", false)
		scope := mapParam(m, "scope")
		_ = stringSliceParam(m, "tags", nil)

		// Defaults must be returned when keys are present with wrong type.
		if v, ok := m["metric"]; ok {
			if _, isString := v.(string); !isString || v == "" {
				if s != "default-metric" {
					t.Fatalf("stringParam returned %q for non-string metric %v", s, v)
				}
			}
		}
		// sqlComparator over the fuzzed value must still respect its allow-list.
		op := sqlComparator(c)
		if op != "" && op != ">" && op != ">=" && op != "<" && op != "<=" {
			t.Fatalf("sqlComparator passed-through unsafe operator %q from %q", op, c)
		}
		// mapParam always returns a non-nil map.
		if scope == nil {
			t.Fatalf("mapParam returned nil map for input %s", data)
		}
	})
}
