// Package identity collects local accounts and authentication events.
//
// Users are read from /etc/passwd directly (no NSS lookups) so we don't pull
// in LDAP/SSSD-managed accounts that don't actually live on this host.
// Group membership for sudo/wheel/admin is resolved via getent group, which
// will respect NSS — that's the right call: a user granted sudo through SSSD
// is still effectively a sudoer on this host.
//
// Login events come from systemd-journal (preferred, structured) with last/
// lastb as a fallback. The collector keeps a per-method cursor so we don't
// re-emit events the server has already seen.
package identity

import (
	"bufio"
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"net"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/pr0ph37/mon/internal/agent/config"
	"github.com/pr0ph37/mon/internal/agent/safeexec"
	"github.com/pr0ph37/mon/internal/shared/apitypes"
)

// redactMask is the placeholder substituted for shell/home paths when the
// corresponding redact toggle is on. Single constant so consumers can grep.
const redactMask = "***"

// Collector implements collector.Source and collector.InventoryProvider.
type Collector struct {
	cursor time.Time // emit only events newer than this
	redact config.RedactConfig
}

// New constructs a Collector. The RedactConfig is applied at the source — if
// Enabled is false (default), the collector behaves exactly as before. We
// thread config in at construction rather than via a global so tests can
// exercise both modes deterministically.
func New(redact config.RedactConfig) *Collector {
	// Default cursor = now-2m so the first tick captures recent events but
	// doesn't dump months of history. The cursor is advanced each tick to
	// the latest event we successfully observed.
	return &Collector{
		cursor: time.Now().Add(-2 * time.Minute).UTC(),
		redact: redact,
	}
}

func (c *Collector) Name() string { return "identity" }

func (c *Collector) Inventory(_ context.Context, snap *apitypes.InventorySnap) error {
	users, err := readPasswd("/etc/passwd")
	if err != nil {
		return err
	}
	sudoers := sudoerSet()
	for i := range users {
		if _, ok := sudoers[users[i].Username]; ok {
			users[i].IsSudoer = true
		}
		users[i].IsSystem = users[i].UID < 1000
		// Defence-in-depth PII redaction. Applied per-field so operators can
		// keep e.g. shells (useful for "is this a real login account?") while
		// dropping homes (often contain usernames mirrored from corp
		// directories).
		if c.redact.Enabled {
			if c.redact.Shells {
				users[i].Shell = redactMask
			}
			if c.redact.Homes {
				users[i].Home = redactMask
			}
		}
	}
	snap.Users = users
	return nil
}

func (c *Collector) Collect(ctx context.Context, batch *apitypes.IngestRequest) error {
	events, latest, err := c.readJournal(ctx)
	if err != nil || len(events) == 0 {
		// Fallback to last/lastb only if journal returned nothing — saves cycles
		// on systemd hosts which are the common case.
		fb, fbLatest := c.readWtmp(ctx)
		events = append(events, fb...)
		if fbLatest.After(latest) {
			latest = fbLatest
		}
	}
	if len(events) == 0 {
		return nil
	}
	// Hash source IPs before they enter the outbound batch. We use the first
	// 8 hex chars of sha256 — enough to correlate repeat offenders (same IP
	// across events) without disclosing the address itself. Empty IPs stay
	// empty so the dashboard's "no source recorded" state is preserved.
	if c.redact.Enabled && c.redact.SourceIPs {
		for i := range events {
			if events[i].SourceIP != "" {
				events[i].SourceIP = hashIP(events[i].SourceIP)
			}
		}
	}
	batch.Logins = append(batch.Logins, events...)
	if latest.After(c.cursor) {
		c.cursor = latest
	}
	return nil
}

// hashIP returns the first 8 hex chars of sha256(ip). Short enough to keep
// the field human-glanceable in logs while one-way enough to defeat casual
// rainbow-table attacks against the much larger search space of public IPs.
func hashIP(ip string) string {
	sum := sha256.Sum256([]byte(ip))
	return hex.EncodeToString(sum[:])[:8]
}

// --- /etc/passwd parser ---------------------------------------------------

func readPasswd(path string) ([]apitypes.UserInfo, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()

	var out []apitypes.UserInfo
	sc := bufio.NewScanner(f)
	for sc.Scan() {
		line := sc.Text()
		if strings.HasPrefix(line, "#") || line == "" {
			continue
		}
		// name:passwd:uid:gid:gecos:home:shell
		f := strings.Split(line, ":")
		if len(f) < 7 {
			continue
		}
		uid, _ := strconv.Atoi(f[2])
		gid, _ := strconv.Atoi(f[3])
		out = append(out, apitypes.UserInfo{
			Username: f[0],
			UID:      uid,
			GID:      gid,
			Home:     f[5],
			Shell:    f[6],
		})
	}
	return out, sc.Err()
}

// sudoerSet returns the set of usernames in admin-like groups. We probe a
// few group names because distros disagree (Debian: sudo; RHEL/Arch: wheel;
// Ubuntu also: admin).
func sudoerSet() map[string]struct{} {
	out := map[string]struct{}{}
	for _, group := range []string{"sudo", "wheel", "admin"} {
		raw, err := safeexec.RunWithTimeout(context.Background(), 3*time.Second, "getent", "group", group)
		if err != nil || len(raw) == 0 {
			continue
		}
		// Format: "group:x:gid:user1,user2,..."
		fields := strings.SplitN(strings.TrimSpace(string(raw)), ":", 4)
		if len(fields) < 4 {
			continue
		}
		for _, u := range strings.Split(fields[3], ",") {
			u = strings.TrimSpace(u)
			if u != "" {
				out[u] = struct{}{}
			}
		}
	}
	return out
}

// --- journal-based login event extractor ----------------------------------

type journalEntry struct {
	RealtimeUS string `json:"__REALTIME_TIMESTAMP"`
	Message    string `json:"MESSAGE"`
	Unit       string `json:"_SYSTEMD_UNIT"`
	Comm       string `json:"_COMM"`
}

// readJournal pulls structured sshd auth lines since the cursor. We narrow to
// _COMM=sshd to skip the whole world; that misses other auth surfaces (login,
// gdm) but those are rare on servers and add a lot of parsing work.
func (c *Collector) readJournal(ctx context.Context) ([]apitypes.LoginEvent, time.Time, error) {
	if !safeexec.Available("journalctl") {
		return nil, time.Time{}, nil
	}
	since := c.cursor.UTC().Format("2006-01-02 15:04:05")
	out, err := safeexec.RunWithTimeout(ctx, 8*time.Second, "journalctl",
		"_COMM=sshd",
		"--since", since,
		"--output=json",
		"--no-pager",
	)
	if err != nil {
		return nil, time.Time{}, err
	}

	var events []apitypes.LoginEvent
	var latest time.Time
	sc := bufio.NewScanner(bytes.NewReader(out))
	sc.Buffer(make([]byte, 64*1024), 1<<20)
	for sc.Scan() {
		var e journalEntry
		if err := json.Unmarshal(sc.Bytes(), &e); err != nil {
			continue
		}
		ts, err := strconv.ParseInt(e.RealtimeUS, 10, 64)
		if err != nil {
			continue
		}
		t := time.Unix(0, ts*1000).UTC()
		if !t.After(c.cursor) {
			continue
		}
		ev, ok := parseSSHDMessage(t, e.Message)
		if !ok {
			continue
		}
		events = append(events, ev)
		if t.After(latest) {
			latest = t
		}
	}
	return events, latest, sc.Err()
}

// parseSSHDMessage recognizes the common sshd auth lines. We deliberately
// don't attempt to cover every PAM permutation — a missing failed attempt
// hurts less than a false positive that confuses the operator.
func parseSSHDMessage(t time.Time, msg string) (apitypes.LoginEvent, bool) {
	switch {
	case strings.Contains(msg, "Accepted password for"),
		strings.Contains(msg, "Accepted publickey for"),
		strings.Contains(msg, "Accepted keyboard-interactive/pam for"):
		user, ip := extractUserIP(msg, "for ")
		return apitypes.LoginEvent{
			Time: t, Method: "ssh", Success: true,
			Username: user, SourceIP: ip, Detail: msg,
		}, true
	case strings.Contains(msg, "Failed password for"):
		// Format: "Failed password for [invalid user] <user> from <ip> port <p>"
		user, ip := extractUserIP(msg, "for ")
		return apitypes.LoginEvent{
			Time: t, Method: "ssh", Success: false,
			Username: user, SourceIP: ip, Detail: msg,
		}, true
	case strings.Contains(msg, "Invalid user"):
		// "Invalid user <user> from <ip> port <p>"
		user, ip := extractUserIP(msg, "user ")
		return apitypes.LoginEvent{
			Time: t, Method: "ssh", Success: false,
			Username: user, SourceIP: ip, Detail: msg,
		}, true
	}
	return apitypes.LoginEvent{}, false
}

// extractUserIP scans for "<keyword> <user> from <ip>" and returns user, ip.
// "invalid user" prefix is stripped if present.
func extractUserIP(msg, keyword string) (string, string) {
	idx := strings.Index(msg, keyword)
	if idx < 0 {
		return "", ""
	}
	tail := msg[idx+len(keyword):]
	tail = strings.TrimPrefix(tail, "invalid user ")
	fields := strings.Fields(tail)
	if len(fields) == 0 {
		return "", ""
	}
	user := fields[0]
	ip := ""
	for i := 1; i < len(fields)-1; i++ {
		if fields[i] == "from" {
			candidate := fields[i+1]
			if net.ParseIP(candidate) != nil {
				ip = candidate
			}
			break
		}
	}
	return user, ip
}

// --- last/lastb wtmp fallback ---------------------------------------------

func (c *Collector) readWtmp(ctx context.Context) ([]apitypes.LoginEvent, time.Time) {
	var events []apitypes.LoginEvent
	var latest time.Time

	if safeexec.Available("last") {
		ev := parseWtmpDump(ctx, "last", true, c.cursor)
		events = append(events, ev...)
		for _, e := range ev {
			if e.Time.After(latest) {
				latest = e.Time
			}
		}
	}
	if safeexec.Available("lastb") {
		ev := parseWtmpDump(ctx, "lastb", false, c.cursor)
		events = append(events, ev...)
		for _, e := range ev {
			if e.Time.After(latest) {
				latest = e.Time
			}
		}
	}
	return events, latest
}

// parseWtmpDump runs last/lastb with --fulltimes and parses the result. We
// pass --time-format=iso for a parseable timestamp; older util-linux versions
// use --fullnames-fulltimes; we try the modern flag first.
func parseWtmpDump(ctx context.Context, cmd string, success bool, cursor time.Time) []apitypes.LoginEvent {
	out, err := safeexec.RunWithTimeout(ctx, 5*time.Second, cmd,
		"--time-format=iso", "-i", "-w")
	if err != nil {
		return nil
	}
	var events []apitypes.LoginEvent
	sc := bufio.NewScanner(bytes.NewReader(out))
	for sc.Scan() {
		line := sc.Text()
		// Header/footer lines we don't want.
		if strings.HasPrefix(line, "wtmp begins") || strings.HasPrefix(line, "btmp begins") || line == "" {
			continue
		}
		f := strings.Fields(line)
		if len(f) < 5 {
			continue
		}
		// Heuristic: user, tty, source(may be blank or "-"), date in ISO,
		// then maybe "still logged in" or duration.
		user := f[0]
		var ip string
		var ts time.Time
		// Find first ISO timestamp field.
		for i := 2; i < len(f); i++ {
			if t, err := time.Parse("2006-01-02T15:04:05-0700", f[i]); err == nil {
				ts = t.UTC()
				if i > 2 && f[i-1] != "-" {
					ip = f[i-1]
				}
				break
			}
		}
		if ts.IsZero() || !ts.After(cursor) {
			continue
		}
		events = append(events, apitypes.LoginEvent{
			Time:     ts,
			Method:   ttyMethod(f[1]),
			Success:  success,
			Username: user,
			SourceIP: ip,
			Detail:   line,
		})
	}
	return events
}

func ttyMethod(tty string) string {
	switch {
	case strings.HasPrefix(tty, "pts"):
		return "ssh"
	case strings.HasPrefix(tty, "tty"):
		return "login"
	}
	return "login"
}
