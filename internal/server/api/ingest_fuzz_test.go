package api

import (
	"encoding/json"
	"testing"

	"github.com/MalteKiefer/MonSys/internal/shared/apitypes"
)

// FuzzIngestRequestDecode exercises apitypes.IngestRequest JSON decoding with
// arbitrary bytes. Contract: decoding must never panic. We do not assert that
// every fuzzer-generated blob is a valid request — that's what the handler-side
// validation is for; here we just lock in that the decoder itself is robust to
// malformed/adversarial input.
func FuzzIngestRequestDecode(f *testing.F) {
	// Three known-good real shapes.
	f.Add([]byte(`{"snapshot_at":"2026-05-12T10:00:00Z"}`))
	f.Add([]byte(`{"snapshot_at":"2026-05-12T10:00:00Z","system":[{"time":"2026-05-12T10:00:00Z","cpu_usage_pct":12.5}]}`))
	f.Add([]byte(`{"snapshot_at":"2026-05-12T10:00:00Z","inventory":{"hostname":"h","kernel":"6.0","distro":"d","agent_version":"v1","cpu_model":"m","cpu_cores":1,"ram_total_bytes":1024}}`))
	// Hostile shapes the decoder must shrug off.
	f.Add([]byte(`{}`))
	f.Add([]byte(`null`))
	f.Add([]byte(``))
	f.Add([]byte(`{"snapshot_at":1234567890}`))
	f.Add([]byte(`{"system":"not-an-array"}`))
	f.Add([]byte("\x00\x00\x00"))

	f.Fuzz(func(t *testing.T, data []byte) {
		var req apitypes.IngestRequest
		// Decode-only contract — error is expected for most blobs and ignored.
		// What matters is that json.Unmarshal does not panic.
		_ = json.Unmarshal(data, &req)
	})
}
