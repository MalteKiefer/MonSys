package apitypes

import (
	"encoding/json"
	"testing"
	"time"
)

func TestMailReportRoundTrip(t *testing.T) {
	in := IngestRequest{Mail: &MailReport{
		Time:     time.Unix(1_700_000_000, 0).UTC(),
		Services: []MailService{{Name: "postfix", Active: true, SubState: "running"}},
		Queue:    &PostfixQueue{Active: 1, Deferred: 4, Hold: 0, Incoming: 0, Total: 5},
		Rspamd:   &RspamdStat{Reachable: true, Scanned: 100, Rejected: 3, Greylisted: 7, Learned: 2},
		Ports:    []MailPortCheck{{Port: 993, Proto: "imap", Open: true, TLS: true}},
	}}
	b, err := json.Marshal(in)
	if err != nil {
		t.Fatal(err)
	}
	var out IngestRequest
	if err := json.Unmarshal(b, &out); err != nil {
		t.Fatal(err)
	}
	if out.Mail == nil || out.Mail.Queue.Total != 5 || out.Mail.Rspamd.Greylisted != 7 {
		t.Fatalf("round trip lost data: %+v", out.Mail)
	}
}
