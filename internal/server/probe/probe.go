// Package probe implements active monitors that the server runs on a
// schedule: TLS cert expiry, database reachability (postgres / mysql /
// mongodb), generic HTTP, and raw TCP connect.
//
// Each backend takes a Probe (the parsed monitor row) and returns a Result.
// Probes do not access the DB themselves; the scheduler persists the result.
package probe

import (
	"context"
	"crypto/tls"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"os"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
)

const (
	StatusOK      = "ok"
	StatusWarn    = "warn"
	StatusFail    = "fail"
	StatusUnknown = "unknown"
)

type Probe struct {
	Type     string
	Target   string
	Params   map[string]any
	Timeout  time.Duration
}

type Result struct {
	Status    string
	LatencyMS int
	Detail    string
}

// Run dispatches to the right backend. Unknown types return "unknown".
func Run(ctx context.Context, p Probe) Result {
	if p.Timeout <= 0 {
		p.Timeout = 10 * time.Second
	}
	ctx, cancel := context.WithTimeout(ctx, p.Timeout)
	defer cancel()

	start := time.Now()
	switch strings.ToLower(p.Type) {
	case "cert":
		return finish(start, runCert(ctx, p))
	case "tcp":
		return finish(start, runTCP(ctx, p))
	case "http":
		return finish(start, runHTTP(ctx, p))
	case "postgres":
		return finish(start, runPostgres(ctx, p))
	case "mysql", "mariadb":
		return finish(start, runMySQL(ctx, p))
	case "mongodb":
		return finish(start, runMongoDB(ctx, p))
	}
	return Result{Status: StatusUnknown, Detail: "unsupported probe type: " + p.Type}
}

func finish(start time.Time, r Result) Result {
	if r.LatencyMS == 0 {
		r.LatencyMS = int(time.Since(start) / time.Millisecond)
	}
	return r
}

// --- SSRF guard ------------------------------------------------------------

// privateCIDRs lists ranges that are typically off-limits for probes when
// the operator opts into the deny mode: RFC1918, loopback, link-local,
// IPv6 ULA, and IPv6 link-local. The cloud metadata endpoint
// (169.254.169.254) is covered by 169.254.0.0/16.
var privateCIDRs = func() []*net.IPNet {
	cidrs := []string{
		"127.0.0.0/8",
		"10.0.0.0/8",
		"172.16.0.0/12",
		"192.168.0.0/16",
		"169.254.0.0/16",
		"::1/128",
		"fc00::/7",
		"fe80::/10",
	}
	out := make([]*net.IPNet, 0, len(cidrs))
	for _, c := range cidrs {
		_, n, err := net.ParseCIDR(c)
		if err == nil {
			out = append(out, n)
		}
	}
	return out
}()

// denyDestination resolves host (which may be a bare hostname or contain a
// port) and rejects probes whose targets fall inside privateCIDRs.
//
// Default policy: allow internal targets. Operators routinely run probes
// against internal services (DBs on RFC1918 networks, etc.), so the deny
// list is opt-in. Set MON_PROBE_ALLOW_INTERNAL=0 or MON_PROBE_DENY_INTERNAL=1
// to enable. All resolved IPs are checked, which partially mitigates DNS
// rebinding (a hostname returning multiple A records mixing public and
// private addresses is rejected if any is private).
func denyDestination(host string) error {
	if host == "" {
		return nil
	}
	allow := os.Getenv("MON_PROBE_ALLOW_INTERNAL")
	deny := os.Getenv("MON_PROBE_DENY_INTERNAL")
	// Default = allow. Only enforce when the operator opts in.
	if !(allow == "0" || deny == "1") {
		return nil
	}

	// Strip optional :port suffix. net.SplitHostPort fails on bare hosts,
	// so try it but fall back to the raw string.
	h := host
	if hh, _, err := net.SplitHostPort(host); err == nil {
		h = hh
	}
	// Strip IPv6 brackets if any survived.
	h = strings.TrimPrefix(strings.TrimSuffix(h, "]"), "[")

	var ips []net.IP
	if ip := net.ParseIP(h); ip != nil {
		ips = []net.IP{ip}
	} else {
		resolved, err := net.LookupIP(h)
		if err != nil {
			return fmt.Errorf("resolve %q: %w", h, err)
		}
		ips = resolved
	}
	for _, ip := range ips {
		for _, n := range privateCIDRs {
			if n.Contains(ip) {
				return errors.New("destination is in a denied range")
			}
		}
	}
	return nil
}

// targetHost extracts a hostname (without port) from either a URL or a
// host[:port] string. Returns "" when nothing usable is present.
func targetHost(s string) string {
	if s == "" {
		return ""
	}
	if u, err := url.Parse(s); err == nil && u.Host != "" {
		if h, _, err := net.SplitHostPort(u.Host); err == nil {
			return h
		}
		return u.Host
	}
	if h, _, err := net.SplitHostPort(s); err == nil {
		return h
	}
	return s
}

// --- TCP -------------------------------------------------------------------

func runTCP(ctx context.Context, p Probe) Result {
	if err := denyDestination(targetHost(p.Target)); err != nil {
		return Result{Status: StatusFail, Detail: err.Error()}
	}
	var d net.Dialer
	conn, err := d.DialContext(ctx, "tcp", p.Target)
	if err != nil {
		return Result{Status: StatusFail, Detail: err.Error()}
	}
	_ = conn.Close()
	return Result{Status: StatusOK, Detail: "connected"}
}

// --- HTTP ------------------------------------------------------------------

func runHTTP(ctx context.Context, p Probe) Result {
	u, err := url.Parse(p.Target)
	if err != nil {
		return Result{Status: StatusFail, Detail: "invalid url: " + err.Error()}
	}
	if u.Scheme != "http" && u.Scheme != "https" {
		return Result{Status: StatusFail, Detail: "url must be http(s)"}
	}
	if err := denyDestination(u.Hostname()); err != nil {
		return Result{Status: StatusFail, Detail: err.Error()}
	}

	insecure := boolParam(p.Params, "insecure_skip_verify", false)
	expected := intParam(p.Params, "expected_status", 200)
	method := stringParam(p.Params, "method", "GET")

	tr := &http.Transport{
		TLSClientConfig: &tls.Config{
			InsecureSkipVerify: insecure, //nolint:gosec // opt-in via monitor params for self-signed targets
			MinVersion:         tls.VersionTLS12,
		},
	}
	c := &http.Client{Transport: tr, Timeout: p.Timeout}

	req, err := http.NewRequestWithContext(ctx, method, p.Target, nil)
	if err != nil {
		return Result{Status: StatusFail, Detail: err.Error()}
	}
	resp, err := c.Do(req)
	if err != nil {
		return Result{Status: StatusFail, Detail: err.Error()}
	}
	defer resp.Body.Close()
	_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, 16<<10))

	if resp.StatusCode != expected {
		return Result{Status: StatusFail,
			Detail: fmt.Sprintf("status %d (want %d)", resp.StatusCode, expected)}
	}
	return Result{Status: StatusOK, Detail: fmt.Sprintf("status %d", resp.StatusCode)}
}

// --- TLS cert expiry -------------------------------------------------------

// runCert connects to host:port via TLS and inspects the leaf cert. Status
// transitions: <warnDays = warn, <failDays = fail, expired = fail.
func runCert(ctx context.Context, p Probe) Result {
	warnDays := intParam(p.Params, "warn_days", 30)
	failDays := intParam(p.Params, "fail_days", 7)
	serverName := stringParam(p.Params, "server_name", "")

	host, _, err := net.SplitHostPort(p.Target)
	if err != nil {
		return Result{Status: StatusFail, Detail: "target must be host:port: " + err.Error()}
	}
	if err := denyDestination(host); err != nil {
		return Result{Status: StatusFail, Detail: err.Error()}
	}
	if serverName == "" {
		serverName = host
	}

	d := tls.Dialer{
		NetDialer: &net.Dialer{},
		Config: &tls.Config{
			ServerName: serverName,
			MinVersion: tls.VersionTLS12,
		},
	}
	conn, err := d.DialContext(ctx, "tcp", p.Target)
	if err != nil {
		return Result{Status: StatusFail, Detail: err.Error()}
	}
	defer conn.Close()

	tlsConn, ok := conn.(*tls.Conn)
	if !ok {
		return Result{Status: StatusFail, Detail: "internal: connection is not TLS"}
	}
	state := tlsConn.ConnectionState()
	if len(state.PeerCertificates) == 0 {
		return Result{Status: StatusFail, Detail: "no peer certificates"}
	}
	leaf := state.PeerCertificates[0]
	now := time.Now()
	left := leaf.NotAfter.Sub(now)
	leftDays := int(left / (24 * time.Hour))

	detail := fmt.Sprintf("subject=%q issuer=%q expires_in=%dd not_after=%s",
		leaf.Subject.CommonName, leaf.Issuer.CommonName, leftDays, leaf.NotAfter.UTC().Format(time.RFC3339))

	switch {
	case left <= 0:
		return Result{Status: StatusFail, Detail: "expired: " + detail}
	case leftDays < failDays:
		return Result{Status: StatusFail, Detail: detail}
	case leftDays < warnDays:
		return Result{Status: StatusWarn, Detail: detail}
	}
	return Result{Status: StatusOK, Detail: detail}
}

// --- Postgres -------------------------------------------------------------

// runPostgres opens a single pgx connection (NOT a pool — we don't want to
// hold idle conns to the target) and runs SELECT version(). The DSN goes in
// p.Target, e.g. postgres://user:pw@host:5432/db?sslmode=require.
func runPostgres(ctx context.Context, p Probe) Result {
	conn, err := pgx.Connect(ctx, p.Target)
	if err != nil {
		return Result{Status: StatusFail, Detail: "connect: " + err.Error()}
	}
	defer conn.Close(ctx)
	var version string
	if err := conn.QueryRow(ctx, "SELECT version()").Scan(&version); err != nil {
		return Result{Status: StatusFail, Detail: "query: " + err.Error()}
	}
	if len(version) > 200 {
		version = version[:200]
	}
	return Result{Status: StatusOK, Detail: version}
}

// --- MySQL/MariaDB --------------------------------------------------------

// runMySQL implements a minimal handshake-only check: dial the TCP port and
// read the server's initial handshake packet. We avoid pulling go-sql-driver/
// mysql to keep the dependency footprint small. The handshake leaks the
// version string, which is the most useful "is it really MariaDB?" signal.
func runMySQL(ctx context.Context, p Probe) Result {
	target := p.Target
	// Allow URL-style "mysql://user:pw@host:3306/db" for parity with postgres
	// even though we don't authenticate.
	if u, err := url.Parse(target); err == nil && u.Host != "" {
		target = u.Host
	}
	if !strings.Contains(target, ":") {
		target += ":3306"
	}
	if err := denyDestination(targetHost(target)); err != nil {
		return Result{Status: StatusFail, Detail: err.Error()}
	}

	var d net.Dialer
	conn, err := d.DialContext(ctx, "tcp", target)
	if err != nil {
		return Result{Status: StatusFail, Detail: err.Error()}
	}
	defer conn.Close()
	deadline, ok := ctx.Deadline()
	if !ok {
		deadline = time.Now().Add(5 * time.Second)
	}
	_ = conn.SetReadDeadline(deadline)

	// Packet header is 3-byte little-endian length + 1 byte sequence id.
	hdr := make([]byte, 4)
	if _, err := io.ReadFull(conn, hdr); err != nil {
		return Result{Status: StatusFail, Detail: "no handshake: " + err.Error()}
	}
	plen := int(hdr[0]) | int(hdr[1])<<8 | int(hdr[2])<<16
	if plen <= 0 || plen > 1024 {
		return Result{Status: StatusFail, Detail: fmt.Sprintf("bad handshake length %d", plen)}
	}
	body := make([]byte, plen)
	if _, err := io.ReadFull(conn, body); err != nil {
		return Result{Status: StatusFail, Detail: "short handshake: " + err.Error()}
	}
	// First byte is the protocol version (typically 10). Then null-terminated
	// server version string.
	if len(body) < 2 {
		return Result{Status: StatusFail, Detail: "truncated handshake"}
	}
	end := 1
	for end < len(body) && body[end] != 0 {
		end++
	}
	version := string(body[1:end])
	return Result{Status: StatusOK, Detail: "version=" + version}
}

// --- MongoDB --------------------------------------------------------------

// runMongoDB sends a minimal "isMaster" / "hello" command via the wire
// protocol. We hand-roll OP_MSG to avoid pulling in the full mongo driver.
// This works against MongoDB 3.6+ (wire protocol v6+) which covers anything
// remotely modern.
func runMongoDB(ctx context.Context, p Probe) Result {
	target := p.Target
	if u, err := url.Parse(target); err == nil && u.Host != "" {
		target = u.Host
	}
	if !strings.Contains(target, ":") {
		target += ":27017"
	}
	if err := denyDestination(targetHost(target)); err != nil {
		return Result{Status: StatusFail, Detail: err.Error()}
	}

	var d net.Dialer
	conn, err := d.DialContext(ctx, "tcp", target)
	if err != nil {
		return Result{Status: StatusFail, Detail: err.Error()}
	}
	defer conn.Close()
	deadline, ok := ctx.Deadline()
	if !ok {
		deadline = time.Now().Add(5 * time.Second)
	}
	_ = conn.SetDeadline(deadline)

	pkt, err := buildMongoHello()
	if err != nil {
		return Result{Status: StatusFail, Detail: err.Error()}
	}
	if _, err := conn.Write(pkt); err != nil {
		return Result{Status: StatusFail, Detail: "write: " + err.Error()}
	}

	hdr := make([]byte, 16)
	if _, err := io.ReadFull(conn, hdr); err != nil {
		return Result{Status: StatusFail, Detail: "header: " + err.Error()}
	}
	msgLen := int(uint32(hdr[0]) | uint32(hdr[1])<<8 | uint32(hdr[2])<<16 | uint32(hdr[3])<<24)
	if msgLen < 16 || msgLen > 16<<20 {
		return Result{Status: StatusFail, Detail: fmt.Sprintf("bad msg length %d", msgLen)}
	}
	rest := make([]byte, msgLen-16)
	if _, err := io.ReadFull(conn, rest); err != nil {
		return Result{Status: StatusFail, Detail: "body: " + err.Error()}
	}

	// We don't fully decode BSON; presence of "ok":1 in a small reply is
	// enough to call the server alive. Look for the BSON name "ok" + 0x01.
	if hasMongoOK(rest) {
		return Result{Status: StatusOK, Detail: "hello returned ok=1"}
	}
	return Result{Status: StatusWarn, Detail: "hello reply lacks ok=1 marker"}
}

// buildMongoHello constructs an OP_MSG containing { hello: 1, $db: "admin" }
// in BSON. Hand-rolled because we don't want a BSON dependency for one packet.
func buildMongoHello() ([]byte, error) {
	// BSON document: int32 size; entries; \x00.
	// Entry: type byte, name C-string, value.
	// hello: int32 1 (type 0x10)
	// $db: string "admin" (type 0x02)
	doc := []byte{}
	// hello: 1
	doc = append(doc, 0x10)
	doc = append(doc, []byte("hello")...)
	doc = append(doc, 0x00)
	doc = append(doc, byte(1), 0, 0, 0)
	// $db: "admin"
	doc = append(doc, 0x02)
	doc = append(doc, []byte("$db")...)
	doc = append(doc, 0x00)
	dbName := []byte("admin\x00")
	doc = append(doc, byte(len(dbName)), 0, 0, 0) //nolint:gosec // BSON/OP_MSG little-endian length prefix; values bounded by hardcoded packet shape
	doc = append(doc, dbName...)

	docSize := 4 + len(doc) + 1
	bson := make([]byte, 0, docSize)
	bson = append(bson,
		byte(docSize), byte(docSize>>8), byte(docSize>>16), byte(docSize>>24)) //nolint:gosec // BSON/OP_MSG little-endian length prefix; values bounded by hardcoded packet shape
	bson = append(bson, doc...)
	bson = append(bson, 0x00)

	// OP_MSG: header(16) + flags(4) + sectionKind(1) + bson
	flags := []byte{0, 0, 0, 0}
	sectionKind := byte(0)

	body := make([]byte, 0, 5+len(bson))
	body = append(body, flags...)
	body = append(body, sectionKind)
	body = append(body, bson...)

	totalLen := 16 + len(body)
	hdr := []byte{ //nolint:gosec // BSON/OP_MSG little-endian length prefix; values bounded by hardcoded packet shape
		byte(totalLen), byte(totalLen >> 8), byte(totalLen >> 16), byte(totalLen >> 24),
		1, 0, 0, 0, // requestID
		0, 0, 0, 0, // responseTo
		0xdd, 0x07, 0, 0, // opCode = 2013 (OP_MSG)
	}
	out := make([]byte, 0, totalLen)
	out = append(out, hdr...)
	out = append(out, body...)
	if len(out) != totalLen {
		return nil, errors.New("internal: mongo packet size mismatch")
	}
	return out, nil
}

// hasMongoOK scans for the BSON-encoded sequence: type 0x10 + "ok"+\x00 +
// (int32 1) OR type 0x01 + "ok"+\x00 + (float64 1.0). Either is "ok=1".
func hasMongoOK(buf []byte) bool {
	target := []byte("\x10ok\x00\x01\x00\x00\x00")
	if indexOfBytes(buf, target) >= 0 {
		return true
	}
	// Float64 1.0 little-endian: 0x00 0x00 0x00 0x00 0x00 0x00 0xF0 0x3F
	target = []byte("\x01ok\x00\x00\x00\x00\x00\x00\x00\xF0\x3F")
	return indexOfBytes(buf, target) >= 0
}

func indexOfBytes(haystack, needle []byte) int {
	if len(needle) == 0 {
		return 0
	}
	for i := 0; i+len(needle) <= len(haystack); i++ {
		ok := true
		for j := 0; j < len(needle); j++ {
			if haystack[i+j] != needle[j] {
				ok = false
				break
			}
		}
		if ok {
			return i
		}
	}
	return -1
}

// --- param helpers --------------------------------------------------------

func stringParam(m map[string]any, key, def string) string {
	if v, ok := m[key].(string); ok && v != "" {
		return v
	}
	return def
}

func intParam(m map[string]any, key string, def int) int {
	switch v := m[key].(type) {
	case float64:
		return int(v)
	case int:
		return v
	}
	return def
}

func boolParam(m map[string]any, key string, def bool) bool {
	if v, ok := m[key].(bool); ok {
		return v
	}
	return def
}
