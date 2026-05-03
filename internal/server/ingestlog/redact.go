package ingestlog

import (
	"encoding/json"
	"net"
	"strings"
)

// Redact strips sensitive fields from an agent ingest payload before it is
// stored in the in-memory debug buffer. It is best-effort: on any parse or
// marshal error it returns the original payload unchanged so the buffer never
// loses data due to redaction.
//
// Currently redacted:
//   - inventory.users[].shell      → "***"
//   - inventory.users[].home       → "***"
//   - logins[].source_ip           → IPv4: last octet masked (e.g. 192.168.1.0)
//                                     IPv6: low 80 bits zeroed (best-effort)
//
// inventory.users[].last_login_at is intentionally kept (timestamp, low value).
func Redact(payload []byte) []byte {
	if len(payload) == 0 {
		return payload
	}
	var root any
	if err := json.Unmarshal(payload, &root); err != nil {
		return payload
	}
	obj, ok := root.(map[string]any)
	if !ok {
		return payload
	}

	redactInventoryUsers(obj)
	redactLogins(obj)

	out, err := json.Marshal(obj)
	if err != nil {
		return payload
	}
	return out
}

func redactInventoryUsers(root map[string]any) {
	inv, ok := root["inventory"].(map[string]any)
	if !ok {
		return
	}
	usersRaw, ok := inv["users"].([]any)
	if !ok {
		return
	}
	for _, u := range usersRaw {
		user, ok := u.(map[string]any)
		if !ok {
			continue
		}
		if _, has := user["shell"]; has {
			user["shell"] = "***"
		}
		if _, has := user["home"]; has {
			user["home"] = "***"
		}
	}
}

func redactLogins(root map[string]any) {
	logins, ok := root["logins"].([]any)
	if !ok {
		return
	}
	for _, l := range logins {
		login, ok := l.(map[string]any)
		if !ok {
			continue
		}
		ipRaw, ok := login["source_ip"].(string)
		if !ok {
			continue
		}
		login["source_ip"] = maskIP(ipRaw)
	}
}

// maskIP returns a redacted form of the supplied IP. For IPv4 the last octet
// is set to 0; for IPv6 the low 80 bits (last 10 bytes) are zeroed leaving the
// /48 routing prefix intact. Inputs that don't parse as IPs are returned as
// "***" so we never leak unparsed data.
func maskIP(ip string) string {
	parsed := net.ParseIP(strings.TrimSpace(ip))
	if parsed == nil {
		return "***"
	}
	if v4 := parsed.To4(); v4 != nil {
		v4[3] = 0
		return v4.String()
	}
	v6 := parsed.To16()
	if v6 == nil {
		return "***"
	}
	masked := make(net.IP, net.IPv6len)
	copy(masked, v6)
	for i := 6; i < net.IPv6len; i++ {
		masked[i] = 0
	}
	return masked.String()
}
