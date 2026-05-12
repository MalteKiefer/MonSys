//go:build linux

package identity

import (
	"testing"
	"time"
	"unicode/utf8"
)

// FuzzParseJournalctl exercises parseJournalctl with arbitrary bytes — the
// real input is line-delimited JSON from `journalctl --output=json`, but the
// fuzzer feeds totally arbitrary blobs to stress every branch (scanner buffer,
// json.Unmarshal, timestamp parse, message classifier). Contract: never
// panics; returns events with valid-UTF-8 string fields; latest timestamp is
// either zero or after the cursor.
func FuzzParseJournalctl(f *testing.F) {
	f.Add([]byte(`{"__REALTIME_TIMESTAMP":"1715000000000000","MESSAGE":"Accepted password for alice from 10.0.0.1 port 51234 ssh2","_COMM":"sshd"}`))
	f.Add([]byte(`{"__REALTIME_TIMESTAMP":"1715000000000000","MESSAGE":"Failed password for invalid user bob from 1.2.3.4 port 12345 ssh2","_COMM":"sshd"}`))
	f.Add([]byte("{\"__REALTIME_TIMESTAMP\":\"1\",\"MESSAGE\":\"Invalid user attacker from 5.6.7.8 port 22\"}\n{\"junk\":1}\n"))
	f.Add([]byte(""))
	f.Add([]byte("not json at all\n"))
	f.Add([]byte("{\"__REALTIME_TIMESTAMP\":\"notanumber\",\"MESSAGE\":\"hi\"}\n"))

	cursor := time.Unix(0, 0).UTC()

	f.Fuzz(func(t *testing.T, data []byte) {
		events, latest, err := parseJournalctl(data, cursor)
		// err is permitted (scanner errors on oversized lines), but only if
		// it surfaces. The harness must not panic regardless.
		_ = err

		for _, ev := range events {
			for name, s := range map[string]string{
				"Username": ev.Username, "SourceIP": ev.SourceIP,
				"Method": ev.Method, "Detail": ev.Detail,
			} {
				if !utf8.ValidString(s) {
					t.Fatalf("event field %s not UTF-8: %q (input %q)", name, s, data)
				}
			}
			if ev.Time.Before(cursor) || ev.Time.Equal(cursor) {
				t.Fatalf("event time %v is not after cursor %v (input %q)", ev.Time, cursor, data)
			}
		}
		if !latest.IsZero() && !latest.After(cursor) {
			t.Fatalf("latest %v not after cursor %v (input %q)", latest, cursor, data)
		}
	})
}
