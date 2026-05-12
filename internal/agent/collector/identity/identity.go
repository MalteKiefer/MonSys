//go:build linux

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
//
// This file is Linux-only: it reads /etc/passwd directly and shells out to
// journalctl/getent/last/lastb. The build tag at the top excludes it from
// non-Linux builds so cross-compiles don't need stubs.
package identity

import (
	"bufio"
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/MalteKiefer/MonSys/internal/agent/config"
	"github.com/MalteKiefer/MonSys/internal/agent/safeexec"
	"github.com/MalteKiefer/MonSys/internal/shared/apitypes"
)

// Tunables. Kept as named constants so the operational knobs are visible at
// the top of the file rather than buried in call sites.
const (
	// redactMask is the placeholder substituted for shell/home paths when the
	// corresponding redact toggle is on. Single constant so consumers can grep.
	redactMask = "***"

	// hashedIPPrefixLen is the number of leading hex chars of sha256(ip)
	// retained for correlation. Short enough to keep logs glanceable, long
	// enough to make casual collisions unlikely.
	hashedIPPrefixLen = 8

	// initialCursorLookback is how far back the first tick scans. Captures
	// recent events without dumping months of history; subsequent ticks
	// advance the cursor to the latest observed event.
	initialCursorLookback = 2 * time.Minute

	// getentTimeout bounds each `getent group` probe.
	getentTimeout = 3 * time.Second

	// journalctlTimeout bounds the journalctl --since query.
	journalctlTimeout = 8 * time.Second

	// wtmpDumpTimeout bounds each last/lastb invocation.
	wtmpDumpTimeout = 5 * time.Second

	// journalScannerMaxLine is the per-line ceiling for the journalctl JSON
	// scanner. Journal MESSAGE fields can be large (full sshd debug dumps);
	// 1 MiB is well above any realistic auth line.
	journalScannerMaxLine = 1 << 20

	// journalScannerInitBuf is the initial scanner buffer; bufio grows up to
	// journalScannerMaxLine as needed.
	journalScannerInitBuf = 64 * 1024

	// passwdMinFields is the minimum colon-separated fields expected in an
	// /etc/passwd record (name:passwd:uid:gid:gecos:home:shell).
	passwdMinFields = 7

	// getentGroupMinFields is the minimum colon-separated fields in a getent
	// group line (group:x:gid:user1,user2,...).
	getentGroupMinFields = 4

	// wtmpMinFields is the minimum whitespace-separated fields we require in
	// a last/lastb row before attempting to parse it.
	wtmpMinFields = 5

	// systemUIDCeiling is the conventional upper bound for system accounts.
	// UIDs below this are flagged IsSystem.
	systemUIDCeiling = 1000

	// passwdPath is the file we read user records from.
	passwdPath = "/etc/passwd"

	// wtmpISOTimeFormat is the timestamp format produced by
	// `last --time-format=iso`.
	wtmpISOTimeFormat = "2006-01-02T15:04:05-0700"

	// journalSinceFormat is the timestamp format journalctl --since accepts.
	journalSinceFormat = "2006-01-02 15:04:05"
)

// ErrNoJournalctl is returned when journalctl is not on PATH. Treated as a
// soft signal — the caller falls back to last/lastb.
var ErrNoJournalctl = errors.New("identity: journalctl not available")

// adminGroups are the group names checked for sudo membership. Distros
// disagree (Debian: sudo; RHEL/Arch: wheel; Ubuntu also: admin) so we probe
// all three.
var adminGroups = []string{"sudo", "wheel", "admin"}

// logger is the component-scoped slog logger. Every log line from this
// package carries component=identity for easy filtering.
var logger = slog.With("component", "identity")

// Collector implements collector.Source and collector.InventoryProvider for
// local user accounts and authentication events.
type Collector struct {
	cursor time.Time // emit only events newer than this
	redact config.RedactConfig
}

// New constructs a Collector. The RedactConfig is applied at the source — if
// Enabled is false (default), the collector behaves exactly as before. We
// thread config in at construction rather than via a global so tests can
// exercise both modes deterministically.
func New(redact config.RedactConfig) *Collector {
	return &Collector{
		cursor: time.Now().Add(-initialCursorLookback).UTC(),
		redact: redact,
	}
}

// Name returns the collector identifier used by the scheduler and metrics.
func (c *Collector) Name() string { return "identity" }

// Inventory populates snap.Users with the local accounts from /etc/passwd,
// marking sudoers (via getent against adminGroups) and system accounts
// (UID < systemUIDCeiling). Redaction of Shell/Home happens inline here per
// c.redact so the masked values never leave the collector.
func (c *Collector) Inventory(_ context.Context, snap *apitypes.InventorySnap) error {
	users, err := readPasswd(passwdPath)
	if err != nil {
		return fmt.Errorf("identity: read %s: %w", passwdPath, err)
	}
	sudoers := sudoerSet()
	for i := range users {
		if _, ok := sudoers[users[i].Username]; ok {
			users[i].IsSudoer = true
		}
		users[i].IsSystem = users[i].UID < systemUIDCeiling
		// PII redaction applied per-field here (the source) so operators can
		// keep e.g. shells (useful for "is this a real login account?") while
		// dropping homes (often contain usernames mirrored from corp
		// directories). Masked values never leave this function unredacted.
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

// Collect appends new login events to batch.Logins. Journal is preferred; if
// it returns nothing the wtmp fallback runs. Source-IP hashing (when
// c.redact.SourceIPs is set) is applied at this boundary — the raw IP never
// leaves the collector once redaction is enabled.
func (c *Collector) Collect(ctx context.Context, batch *apitypes.IngestRequest) error {
	events, latest, err := c.readJournal(ctx)
	if err != nil && !errors.Is(err, ErrNoJournalctl) {
		logger.Debug("journal read failed", "err", err)
	}
	if len(events) == 0 {
		// Fallback to last/lastb only if journal returned nothing — saves
		// cycles on systemd hosts which are the common case.
		fb, fbLatest := c.readWtmp(ctx)
		events = append(events, fb...)
		if fbLatest.After(latest) {
			latest = fbLatest
		}
	}
	if len(events) == 0 {
		return nil
	}
	// PII redaction: hash source IPs before they enter the outbound batch.
	// hashIP returns hashedIPPrefixLen hex chars of sha256(ip) — enough to
	// correlate repeat offenders without disclosing the address. Empty IPs
	// stay empty so the dashboard's "no source recorded" state is preserved.
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

// hashIP returns the first hashedIPPrefixLen hex chars of sha256(ip). One-way
// enough to defeat casual rainbow-table attacks against the public-IP search
// space while keeping the field human-glanceable in logs.
func hashIP(ip string) string {
	sum := sha256.Sum256([]byte(ip))
	return hex.EncodeToString(sum[:])[:hashedIPPrefixLen]
}

// --- /etc/passwd parser ---------------------------------------------------

// readPasswd parses an /etc/passwd-format file into UserInfo records. Lines
// shorter than passwdMinFields or starting with '#' are skipped. UID/GID
// parse failures fall back to zero (matching the historical behaviour); we
// do not return an error for malformed rows because a single corrupt line
// shouldn't drop the entire inventory.
func readPasswd(path string) ([]apitypes.UserInfo, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, fmt.Errorf("open passwd: %w", err)
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
		fields := strings.Split(line, ":")
		if len(fields) < passwdMinFields {
			continue
		}
		uid, _ := strconv.Atoi(fields[2])
		gid, _ := strconv.Atoi(fields[3])
		out = append(out, apitypes.UserInfo{
			Username: fields[0],
			UID:      uid,
			GID:      gid,
			Home:     fields[5],
			Shell:    fields[6],
		})
	}
	if err := sc.Err(); err != nil {
		return nil, fmt.Errorf("scan passwd: %w", err)
	}
	return out, nil
}

// sudoerSet returns the set of usernames in admin-like groups (see
// adminGroups). Probing multiple group names accommodates distro variance.
func sudoerSet() map[string]struct{} {
	out := map[string]struct{}{}
	for _, group := range adminGroups {
		raw, err := safeexec.RunWithTimeout(context.Background(), getentTimeout, "getent", "group", group)
		if err != nil || len(raw) == 0 {
			continue
		}
		// Format: "group:x:gid:user1,user2,..."
		fields := strings.SplitN(strings.TrimSpace(string(raw)), ":", getentGroupMinFields)
		if len(fields) < getentGroupMinFields {
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

// journalEntry is the subset of fields we read from journalctl --output=json.
// Field names are the dunder-prefixed names used by the journal export format.
type journalEntry struct {
	RealtimeUS string `json:"__REALTIME_TIMESTAMP"`
	Message    string `json:"MESSAGE"`
	Unit       string `json:"_SYSTEMD_UNIT"`
	Comm       string `json:"_COMM"`
}

// readJournal pulls structured sshd auth lines since the cursor. We narrow to
// _COMM=sshd to skip the whole world; that misses other auth surfaces (login,
// gdm) but those are rare on servers and add a lot of parsing work. Returns
// ErrNoJournalctl when the binary is missing so the caller can branch
// cleanly to the wtmp fallback.
func (c *Collector) readJournal(ctx context.Context) ([]apitypes.LoginEvent, time.Time, error) {
	if !safeexec.Available("journalctl") {
		return nil, time.Time{}, ErrNoJournalctl
	}
	since := c.cursor.UTC().Format(journalSinceFormat)
	out, err := safeexec.RunWithTimeout(ctx, journalctlTimeout, "journalctl",
		"_COMM=sshd",
		"--since", since,
		"--output=json",
		"--no-pager",
	)
	if err != nil {
		return nil, time.Time{}, fmt.Errorf("journalctl: %w", err)
	}
	return parseJournalctl(out, c.cursor)
}

// parseJournalctl decodes line-delimited journal JSON into LoginEvents,
// dropping entries at or before cursor and entries that don't match any
// sshd pattern.
func parseJournalctl(out []byte, cursor time.Time) ([]apitypes.LoginEvent, time.Time, error) {
	var events []apitypes.LoginEvent
	var latest time.Time
	sc := bufio.NewScanner(bytes.NewReader(out))
	sc.Buffer(make([]byte, journalScannerInitBuf), journalScannerMaxLine)
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
		if !t.After(cursor) {
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
	if err := sc.Err(); err != nil {
		return events, latest, fmt.Errorf("scan journal: %w", err)
	}
	return events, latest, nil
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

// readWtmp runs `last` (successful logins) and `lastb` (failed) and merges
// the parsed events. Returns the combined slice and the timestamp of the
// newest entry observed.
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

// parseWtmpDump runs last/lastb with --time-format=iso and parses the result.
// success encodes whether the source command was last (true) or lastb (false).
func parseWtmpDump(ctx context.Context, cmd string, success bool, cursor time.Time) []apitypes.LoginEvent {
	out, err := safeexec.RunWithTimeout(ctx, wtmpDumpTimeout, cmd,
		"--time-format=iso", "-i", "-w")
	if err != nil {
		return nil
	}
	return parseWtmp(out, success, cursor)
}

// parseWtmp parses the text output of last/lastb. Split from parseWtmpDump so
// the parser is unit-testable without shelling out.
func parseWtmp(out []byte, success bool, cursor time.Time) []apitypes.LoginEvent {
	var events []apitypes.LoginEvent
	sc := bufio.NewScanner(bytes.NewReader(out))
	for sc.Scan() {
		line := sc.Text()
		// Header/footer lines we don't want.
		if strings.HasPrefix(line, "wtmp begins") || strings.HasPrefix(line, "btmp begins") || line == "" {
			continue
		}
		fields := strings.Fields(line)
		if len(fields) < wtmpMinFields {
			continue
		}
		// Heuristic: user, tty, source(may be blank or "-"), date in ISO,
		// then maybe "still logged in" or duration.
		user := fields[0]
		var ip string
		var ts time.Time
		// Find first ISO timestamp field.
		for i := 2; i < len(fields); i++ {
			if t, err := time.Parse(wtmpISOTimeFormat, fields[i]); err == nil {
				ts = t.UTC()
				if i > 2 && fields[i-1] != "-" {
					ip = fields[i-1]
				}
				break
			}
		}
		if ts.IsZero() || !ts.After(cursor) {
			continue
		}
		events = append(events, apitypes.LoginEvent{
			Time:     ts,
			Method:   ttyMethod(fields[1]),
			Success:  success,
			Username: user,
			SourceIP: ip,
			Detail:   line,
		})
	}
	return events
}

// ttyMethod classifies a tty name into the LoginEvent.Method label. Pseudo-
// terminals (pts/*) are virtually always ssh in the wtmp record we see;
// hardware ttys (tty*) are local console logins.
func ttyMethod(tty string) string {
	switch {
	case strings.HasPrefix(tty, "pts"):
		return "ssh"
	case strings.HasPrefix(tty, "tty"):
		return "login"
	}
	return "login"
}
