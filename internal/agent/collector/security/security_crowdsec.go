//go:build linux

package security

import (
	"context"
	"encoding/json"
	"strconv"
	"time"

	"github.com/MalteKiefer/MonSys/internal/agent/safeexec"
	"github.com/MalteKiefer/MonSys/internal/shared/apitypes"
)

// cscliDecisionItem is one decision row in the JSON emitted by
// `cscli decisions list -o json`. cscli emits two shapes historically:
//
//   - Modern (1.5+): a flat or nested array of alerts, each carrying a
//     `decisions` slice with a `duration` field ("10m0s").
//   - Older:        a flat array of decisions with an `until` RFC3339 field
//     in place of `duration`.
//
// We accept both by reading every field this struct declares; missing fields
// stay zero and computeUntil falls back appropriately.
type cscliDecisionItem struct {
	ID       int    `json:"id"`
	Origin   string `json:"origin"`
	Type     string `json:"type"`
	Scope    string `json:"scope"`
	Value    string `json:"value"`
	Scenario string `json:"scenario"`
	Duration string `json:"duration"`
	Until    string `json:"until"`
}

// cscliAlert is the outer alert wrapper used by cscli's nested JSON shape.
// Only the fields the collector consumes are declared; the `events` slice and
// other metadata are deliberately ignored to keep the unmarshal cheap.
type cscliAlert struct {
	CreatedAt string              `json:"created_at"`
	Decisions []cscliDecisionItem `json:"decisions"`
}

// crowdsecDecisions returns the list of active CrowdSec decisions by parsing
// `cscli decisions list -o json`. It tolerates both the nested-alert and the
// flat-decision JSON layouts cscli has shipped, falling back to flat when the
// nested unmarshal yields no usable rows.
func (c *Collector) crowdsecDecisions(ctx context.Context) []apitypes.CrowdsecDecision {
	if !safeexec.Available("cscli") {
		return nil
	}
	out, err := safeexec.RunWithTimeout(ctx, crowdsecCmdTimeout, "cscli", "decisions", "list", "-o", "json")
	if err != nil {
		logger.Debug("cscli decisions list failed", "err", err)
		return nil
	}

	alerts, rawFlat, ok := parseCscliOutput(out)
	if !ok {
		return nil
	}

	decisions := make([]apitypes.CrowdsecDecision, 0, 8)
	add := func(d cscliDecisionItem, parentCreatedAt string) {
		decisions = append(decisions, apitypes.CrowdsecDecision{
			DecisionID: strconv.Itoa(d.ID),
			Origin:     d.Origin,
			Scope:      d.Scope,
			Target:     d.Value,
			Type:       d.Type,
			Reason:     d.Scenario,
			Until:      computeUntil(d, parentCreatedAt),
		})
	}
	if alerts != nil {
		for _, a := range alerts {
			for _, d := range a.Decisions {
				add(d, a.CreatedAt)
			}
		}
	} else {
		for _, d := range rawFlat {
			add(d, "")
		}
	}
	return decisions
}

// parseCscliOutput tries the nested cscliAlert shape first and falls back to
// the flat cscliDecisionItem array. Exactly one of the returned slices is
// non-nil; ok is false when neither shape unmarshalled successfully.
func parseCscliOutput(out []byte) (alerts []cscliAlert, flat []cscliDecisionItem, ok bool) {
	if err := json.Unmarshal(out, &alerts); err == nil {
		// Heuristic: the nested shape carries either a created_at field or a
		// non-nil Decisions slice. If the first element has neither, we got
		// the flat array (which json.Unmarshal silently accepted as a
		// []cscliAlert with all fields empty).
		if len(alerts) == 0 || alerts[0].Decisions != nil || alerts[0].CreatedAt != "" {
			return alerts, nil, true
		}
	}
	if err := json.Unmarshal(out, &flat); err != nil {
		return nil, nil, false
	}
	return nil, flat, true
}

// computeUntil resolves a decision's expiry to an absolute timestamp.
// Preference order:
//
//  1. explicit "until" RFC3339 timestamp,
//  2. parent alert's created_at + duration,
//  3. time.Now() + duration.
//
// Returns the zero time when none of the inputs parse; the frontend renders
// that as a dash.
func computeUntil(d cscliDecisionItem, parentCreatedAt string) time.Time {
	if d.Until != "" {
		if t, err := time.Parse(time.RFC3339, d.Until); err == nil {
			return t
		}
	}
	if d.Duration == "" {
		return time.Time{}
	}
	dur, err := time.ParseDuration(d.Duration)
	if err != nil {
		return time.Time{}
	}
	base := time.Now().UTC()
	if parentCreatedAt != "" {
		if t, err := time.Parse(time.RFC3339, parentCreatedAt); err == nil {
			base = t
		}
	}
	return base.Add(dur)
}
