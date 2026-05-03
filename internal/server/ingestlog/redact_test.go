package ingestlog

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestRedact_ShellAndHome(t *testing.T) {
	in := []byte(`{
		"inventory": {
			"users": [
				{"name": "root", "shell": "/bin/bash", "home": "/root", "last_login_at": "2026-01-02T03:04:05Z"},
				{"name": "alice", "shell": "/bin/zsh", "home": "/home/alice"}
			]
		}
	}`)

	out := Redact(in)
	var got map[string]any
	if err := json.Unmarshal(out, &got); err != nil {
		t.Fatalf("unmarshal redacted: %v", err)
	}

	users := got["inventory"].(map[string]any)["users"].([]any)
	for _, u := range users {
		m := u.(map[string]any)
		if m["shell"] != "***" {
			t.Errorf("shell not redacted: %v", m["shell"])
		}
		if m["home"] != "***" {
			t.Errorf("home not redacted: %v", m["home"])
		}
	}

	root := users[0].(map[string]any)
	if root["last_login_at"] != "2026-01-02T03:04:05Z" {
		t.Errorf("last_login_at should be preserved, got %v", root["last_login_at"])
	}
	if root["name"] != "root" {
		t.Errorf("name should be preserved, got %v", root["name"])
	}

	// Ensure raw secrets do not survive in the serialized output.
	if strings.Contains(string(out), "/bin/bash") || strings.Contains(string(out), "/root") {
		t.Errorf("serialized output still contains raw shell/home: %s", out)
	}
}

func TestRedact_IPv4LastOctet(t *testing.T) {
	in := []byte(`{
		"logins": [
			{"user": "root", "source_ip": "192.168.1.42"},
			{"user": "alice", "source_ip": "10.0.0.7"}
		]
	}`)

	out := Redact(in)
	var got map[string]any
	if err := json.Unmarshal(out, &got); err != nil {
		t.Fatalf("unmarshal redacted: %v", err)
	}

	logins := got["logins"].([]any)
	if ip := logins[0].(map[string]any)["source_ip"]; ip != "192.168.1.0" {
		t.Errorf("ipv4 not masked: %v", ip)
	}
	if ip := logins[1].(map[string]any)["source_ip"]; ip != "10.0.0.0" {
		t.Errorf("ipv4 not masked: %v", ip)
	}

	if strings.Contains(string(out), "192.168.1.42") || strings.Contains(string(out), "10.0.0.7") {
		t.Errorf("raw IP leaked in output: %s", out)
	}
}

func TestRedact_MalformedJSONReturnsInputUnchanged(t *testing.T) {
	in := []byte(`{not valid json`)
	out := Redact(in)
	if string(out) != string(in) {
		t.Errorf("malformed input should be returned unchanged, got %q", out)
	}

	// Same pointer/contents should round-trip even when it's not an object.
	arr := []byte(`[1,2,3]`)
	if string(Redact(arr)) != string(arr) {
		t.Errorf("non-object JSON should be returned unchanged")
	}

	if string(Redact(nil)) != "" {
		t.Errorf("nil input should round-trip empty")
	}
}

func TestRedact_IPv6LowBitsZero(t *testing.T) {
	in := []byte(`{"logins":[{"source_ip":"2001:db8:1234:5678:9abc:def0:1234:5678"}]}`)
	out := Redact(in)
	var got map[string]any
	if err := json.Unmarshal(out, &got); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	ip := got["logins"].([]any)[0].(map[string]any)["source_ip"].(string)
	// Expect /48 prefix preserved, rest zero. net.IP.String collapses zeros.
	if !strings.HasPrefix(ip, "2001:db8:1234:") {
		t.Errorf("ipv6 prefix not preserved: %s", ip)
	}
	if strings.Contains(ip, "9abc") || strings.Contains(ip, "def0") {
		t.Errorf("ipv6 low bits not zeroed: %s", ip)
	}
}
