package mail

import (
	"context"
	"encoding/json"
	"io"
	"net/http"

	"github.com/MalteKiefer/MonSys/internal/shared/apitypes"
)

// rspamdStat fetches the rspamd /stat endpoint and returns a RspamdStat.
// On any error (dial, non-2xx, decode), returns &apitypes.RspamdStat{Reachable:false}.
// On success, decodes the response tolerantly and maps to RspamdStat with Reachable:true.
func rspamdStat(ctx context.Context, client *http.Client, url string) *apitypes.RspamdStat {
	req, err := http.NewRequestWithContext(ctx, "GET", url, http.NoBody)
	if err != nil {
		return &apitypes.RspamdStat{Reachable: false}
	}

	resp, err := client.Do(req)
	if err != nil {
		return &apitypes.RspamdStat{Reachable: false}
	}
	defer resp.Body.Close()

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return &apitypes.RspamdStat{Reachable: false}
	}

	// Bound the body to 64 KiB
	limitedBody := io.LimitReader(resp.Body, 64<<10)

	var statResponse struct {
		Scanned int64 `json:"scanned"`
		Learned int64 `json:"learned"`
		Actions struct {
			Reject   int64 `json:"reject"`
			Greylist int64 `json:"greylist"`
		} `json:"actions"`
	}

	if err := json.NewDecoder(limitedBody).Decode(&statResponse); err != nil {
		return &apitypes.RspamdStat{Reachable: false}
	}

	return &apitypes.RspamdStat{
		Reachable:  true,
		Scanned:    statResponse.Scanned,
		Learned:    statResponse.Learned,
		Rejected:   statResponse.Actions.Reject,
		Greylisted: statResponse.Actions.Greylist,
	}
}
