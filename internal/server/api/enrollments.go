package api

import (
	"context"
	"crypto/sha256"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"strings"
	"time"

	"github.com/danielgtaylor/huma/v2"
	"github.com/google/uuid"
	qrcode "github.com/skip2/go-qrcode"

	"github.com/MalteKiefer/MonSys/internal/server/store"
	"github.com/MalteKiefer/MonSys/internal/shared/apitypes"
)

// --- Enrollment huma I/O types ---------------------------------------------

type createEnrollmentInput struct {
	// Forwarded headers are consulted to derive the canonical install URL the
	// operator copy-pastes onto the target host. We also let the body carry
	// the rest of the policy (label, tags, group ids, ttl).
	XForwardedProto string `header:"X-Forwarded-Proto" doc:"trusted reverse-proxy hint for canonical scheme"`
	XForwardedHost  string `header:"X-Forwarded-Host"  doc:"trusted reverse-proxy hint for canonical host"`
	Host            string `header:"Host"              doc:"raw Host header; used when no X-Forwarded-* is present"`
	Body            apitypes.AgentEnrollmentInput
}

type createEnrollmentOutput struct {
	Body apitypes.AgentEnrollmentCreateResponse
}

type listEnrollmentsInput struct {
	Limit int `query:"limit" minimum:"1" maximum:"200" doc:"page size; default 50"`
}

type listEnrollmentsOutput struct {
	Body struct {
		Enrollments []apitypes.AgentEnrollment `json:"enrollments"`
	}
}

type enrollmentIDInput struct {
	ID string `path:"id" doc:"Enrollment UUID"`
}

type getEnrollmentOutput struct {
	Body struct {
		Enrollment apitypes.AgentEnrollment `json:"enrollment"`
	}
}

// --- Helpers ---------------------------------------------------------------

// enrollBaseURL derives the canonical base URL the agent installer should
// download artefacts from. We trust X-Forwarded-Proto / X-Forwarded-Host when
// either is set (the deploy guidance puts mon-server behind a TLS-terminating
// proxy); otherwise we fall back to https://<r.Host>.
func enrollBaseURL(r *http.Request) string {
	proto := strings.TrimSpace(r.Header.Get("X-Forwarded-Proto"))
	host := strings.TrimSpace(r.Header.Get("X-Forwarded-Host"))
	if host != "" {
		if proto == "" {
			proto = "https"
		}
		return proto + "://" + host
	}
	// No proxy hint — assume the listener is fronted by HTTPS in production.
	// r.Host carries the request authority (host:port) chi/net/http populated
	// from the request line / Host header.
	return "https://" + r.Host
}

// enrollBaseURLFromHeaders mirrors enrollBaseURL but consumes the forwarded
// values huma already parsed for us instead of poking at *http.Request. The
// host fallback is the raw Host header (chi populates it on r.Host before we
// see it; huma surfaces the same value through the "Host" header binding).
func enrollBaseURLFromHeaders(proto, fwdHost, host string) string {
	host = strings.TrimSpace(host)
	fwdHost = strings.TrimSpace(fwdHost)
	proto = strings.TrimSpace(proto)
	if fwdHost != "" {
		if proto == "" {
			proto = "https"
		}
		return proto + "://" + fwdHost
	}
	if host != "" {
		return "https://" + host
	}
	// Last-resort sentinel — should not occur in practice because Go's
	// HTTP server populates Host before we see the request.
	return "https://localhost"
}

// sha256Hash returns sha256(s). Exposed at package scope so handlers can use
// it without reaching into the store package's private hashSecret helper.
func sha256Hash(s string) []byte {
	sum := sha256.Sum256([]byte(s))
	return sum[:]
}

// --- Handlers --------------------------------------------------------------

func (s *Server) handleCreateEnrollment(ctx context.Context, in *createEnrollmentInput) (*createEnrollmentOutput, error) {
	if s.Store == nil {
		return nil, huma.Error503ServiceUnavailable("server has no store configured")
	}
	caller, _ := userFromContext(ctx)
	enrollment, plaintext, err := s.Store.CreateEnrollment(ctx, in.Body, caller.Email)
	if err != nil {
		return nil, internalErr(ctx, "enrollment create failed", err)
	}

	base := enrollBaseURLFromHeaders(in.XForwardedProto, in.XForwardedHost, in.Host)
	installURL := fmt.Sprintf("%s/v1/agents/install.sh?t=%s", base, plaintext)
	installCmd := fmt.Sprintf("curl -fsSL '%s' | sudo bash", installURL)

	// Compact key=value detail so audit log readers can grep on label/tag/group/ttl
	// without parsing JSON. ttl_min reflects the *requested* value; the store
	// clamps to [5,1440] so the on-disk row may differ.
	auditDetail := fmt.Sprintf("label=%q tags=%d groups=%d ttl_min=%d",
		enrollment.Label, len(in.Body.Tags), len(in.Body.GroupIDs), in.Body.TTLMinutes)
	s.audit(ctx, "agent.enroll.create", enrollment.ID, auditDetail)

	out := &createEnrollmentOutput{}
	out.Body.Enrollment = enrollment
	out.Body.Token = plaintext
	out.Body.InstallURL = installURL
	out.Body.InstallCommand = installCmd
	return out, nil
}

func (s *Server) handleListEnrollments(ctx context.Context, in *listEnrollmentsInput) (*listEnrollmentsOutput, error) {
	if s.Store == nil {
		return nil, huma.Error503ServiceUnavailable("server has no store configured")
	}
	limit := in.Limit
	if limit <= 0 {
		limit = 50
	}
	if limit > 200 {
		limit = 200
	}
	since := time.Now().Add(-24 * time.Hour)
	rows, err := s.Store.ListEnrollments(ctx, since, limit)
	if err != nil {
		return nil, internalErr(ctx, "enrollment list failed", err)
	}
	out := &listEnrollmentsOutput{}
	out.Body.Enrollments = rows
	if out.Body.Enrollments == nil {
		out.Body.Enrollments = []apitypes.AgentEnrollment{}
	}
	return out, nil
}

func (s *Server) handleGetEnrollment(ctx context.Context, in *enrollmentIDInput) (*getEnrollmentOutput, error) {
	if s.Store == nil {
		return nil, huma.Error503ServiceUnavailable("server has no store configured")
	}
	id, err := uuid.Parse(in.ID)
	if err != nil {
		return nil, huma.Error400BadRequest("invalid id")
	}
	row, err := s.Store.GetEnrollment(ctx, id)
	if err != nil {
		if errors.Is(err, store.ErrEnrollmentNotFound) {
			return nil, huma.Error404NotFound("enrollment not found")
		}
		return nil, internalErr(ctx, "enrollment get failed", err)
	}
	out := &getEnrollmentOutput{}
	out.Body.Enrollment = row
	return out, nil
}

func (s *Server) handleRevokeEnrollment(ctx context.Context, in *enrollmentIDInput) (*emptyOutput, error) {
	if s.Store == nil {
		return nil, huma.Error503ServiceUnavailable("server has no store configured")
	}
	id, err := uuid.Parse(in.ID)
	if err != nil {
		return nil, huma.Error400BadRequest("invalid id")
	}
	// Look up the enrollment first so the audit row records what was actually
	// revoked (label, used-state). GetEnrollment surfaces ErrEnrollmentNotFound
	// the same way RevokeEnrollment does, so the 404 path is consistent.
	e, err := s.Store.GetEnrollment(ctx, id)
	if err != nil {
		if errors.Is(err, store.ErrEnrollmentNotFound) {
			return nil, huma.Error404NotFound("enrollment not found")
		}
		return nil, internalErr(ctx, "enrollment lookup failed", err)
	}
	if err := s.Store.RevokeEnrollment(ctx, id); err != nil {
		if errors.Is(err, store.ErrEnrollmentNotFound) {
			return nil, huma.Error404NotFound("enrollment not found")
		}
		return nil, internalErr(ctx, "enrollment revoke failed", err)
	}
	used := e.UsedAt != nil
	auditDetail := fmt.Sprintf("label=%q used=%v", e.Label, used)
	s.audit(ctx, "agent.enroll.revoke", in.ID, auditDetail)
	out := &emptyOutput{}
	out.Body.OK = true
	return out, nil
}

// handleInstallQR returns a PNG QR code that encodes the install URL for
// the given token. Same auth model as /v1/agents/install.sh: the token in
// the query string is the proof-of-possession; expired or consumed tokens
// answer 404. The encoded payload is the *install URL* (curl-friendly), so
// scanning the QR with a phone yields the same one-line install command
// path operators copy-paste from the modal.
func (s *Server) handleInstallQR(w http.ResponseWriter, r *http.Request) {
	token := r.URL.Query().Get("t")
	if token == "" || s.Store == nil {
		http.NotFound(w, r)
		return
	}
	hash := sha256Hash(token)
	var used bool
	err := s.Store.Pool.QueryRow(r.Context(),
		`SELECT used_at IS NOT NULL FROM agent_tokens WHERE token_hash = $1 AND expires_at > now()`,
		hash,
	).Scan(&used)
	if err != nil || used {
		http.NotFound(w, r)
		return
	}

	base := enrollBaseURL(r)
	url := fmt.Sprintf("%s/v1/agents/install.sh?t=%s", base, token)
	png, err := qrcode.Encode(url, qrcode.Medium, 320)
	if err != nil {
		slog.Warn("install qr encode", "err", err)
		http.Error(w, "qr encode failed", http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "image/png")
	w.Header().Set("Cache-Control", "no-store")
	_, _ = w.Write(png)
}

// --- Public install script -------------------------------------------------

// handleInstallScript renders the dynamic /v1/agents/install.sh response.
// The script is plaintext — the embedded bootstrap token is no more secret
// than the URL itself (it appears in r.URL.Query). We bypass huma here
// because huma's content negotiation always wraps the body in a typed
// envelope; the installer needs raw shell.
//
// Verifies the token has not been used and is still within its expiry
// window. Anything else returns 404 with a shell-safe failure body so a
// curl|bash pipeline aborts early.
func (s *Server) handleInstallScript(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "text/x-shellscript; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")

	token := r.URL.Query().Get("t")
	if token == "" || s.Store == nil {
		w.WriteHeader(http.StatusNotFound)
		_, _ = w.Write([]byte("# token invalid or already used\nexit 1\n"))
		return
	}

	// Direct-DB lookup keeps the store boundary clean for this read-only
	// gate. A future store helper (LookupBootstrapTokenStatus) could replace
	// this if the same query starts being used elsewhere.
	hash := sha256Hash(token)
	var used bool
	err := s.Store.Pool.QueryRow(r.Context(),
		`SELECT used_at IS NOT NULL FROM agent_tokens WHERE token_hash = $1 AND expires_at > now()`,
		hash,
	).Scan(&used)
	if err != nil || used {
		if err != nil {
			slog.Debug("install.sh token lookup miss", "err", err)
		}
		w.WriteHeader(http.StatusNotFound)
		_, _ = w.Write([]byte("# token invalid or already used\nexit 1\n"))
		return
	}

	base := enrollBaseURL(r)

	// Pull binary metadata so the script can pin sha256s. If the resolver is
	// unset or unreachable we still emit a script — but the agent path falls
	// back to "no auto-update": we hard-fail because the operator expects a
	// functioning binary at the end.
	var amd64URL, amd64Sum, arm64URL, arm64Sum string
	if s.AgentUpdate != nil {
		ctx, cancel := context.WithTimeout(r.Context(), 10*time.Second)
		defer cancel()
		if m, err := s.AgentUpdate.Latest(ctx, false); err == nil && m != nil {
			if b, ok := m.Binaries["linux/amd64"]; ok {
				amd64URL, amd64Sum = b.URL, b.SHA256
			}
			if b, ok := m.Binaries["linux/arm64"]; ok {
				arm64URL, arm64Sum = b.URL, b.SHA256
			}
		} else if err != nil {
			slog.Warn("install.sh: agent update resolver failed", "err", err)
		}
	}
	if amd64URL == "" || amd64Sum == "" || arm64URL == "" || arm64Sum == "" {
		w.WriteHeader(http.StatusServiceUnavailable)
		_, _ = w.Write([]byte("# server has no agent binary metadata published yet\nexit 1\n"))
		return
	}

	script := renderInstallScript(base, token, amd64URL, amd64Sum, arm64URL, arm64Sum)
	_, _ = w.Write([]byte(script))
}

// renderInstallScript stitches the four moving values into the shell template.
// We use fmt.Sprintf rather than text/template so the body remains valid
// shell with literal ${VAR} references; the only interpolations are the
// printf %s slots. Order:
//
//	1: base server URL (e.g. https://mon.example.com)
//	2: bootstrap token (already public via the URL query)
//	3: linux/amd64 binary URL
//	4: linux/amd64 sha256
//	5: linux/arm64 binary URL
//	6: linux/arm64 sha256
func renderInstallScript(base, token, amdURL, amdSHA, armURL, armSHA string) string {
	return fmt.Sprintf(installScriptTemplate, base, token, amdURL, amdSHA, armURL, armSHA)
}

// installScriptTemplate is the dynamic curl|bash installer. Shell ${VAR}
// references are intentionally written so fmt.Sprintf only consumes the six
// %s placeholders we explicitly supply.
const installScriptTemplate = `#!/usr/bin/env bash
# mon-agent one-shot installer.
# This script is rendered server-side; the bootstrap token is embedded
# below. The token expires the moment this script's bootstrap call
# succeeds, so re-running this URL on a second host will fail (by design).
set -euo pipefail

SERVER_URL="%s"
BOOTSTRAP_TOKEN="%s"
BIN_AMD64_URL="%s"
BIN_AMD64_SHA="%s"
BIN_ARM64_URL="%s"
BIN_ARM64_SHA="%s"

# 1. arch detection
uname_m="$(uname -m)"
case "${uname_m}" in
  x86_64|amd64)  ARCH=amd64 ; BIN_URL="${BIN_AMD64_URL}" ; BIN_SHA="${BIN_AMD64_SHA}" ;;
  aarch64|arm64) ARCH=arm64 ; BIN_URL="${BIN_ARM64_URL}" ; BIN_SHA="${BIN_ARM64_SHA}" ;;
  *) echo "unsupported architecture: ${uname_m}" >&2 ; exit 1 ;;
esac

# 2. systemd presence (the unit file we install needs it)
if ! command -v systemctl >/dev/null 2>&1; then
  echo "systemd not detected; mon-agent installer requires systemctl" >&2
  exit 1
fi

# 3. user + filesystem layout
if ! id -u monagent >/dev/null 2>&1; then
  useradd --system --no-create-home --shell /usr/sbin/nologin monagent
fi
install -d -o root     -g monagent -m 0750 /etc/mon-agent
install -d -o monagent -g monagent -m 0750 /var/lib/mon-agent /var/log/mon-agent

# 4. binary download + sha256 verify + atomic move
TMPBIN="$(mktemp -t mon-agent.XXXXXX)"
trap 'rm -f "${TMPBIN}"' EXIT
curl -fsSL "${BIN_URL}" -o "${TMPBIN}"
echo "${BIN_SHA}  ${TMPBIN}" | sha256sum -c -
chmod 0755 "${TMPBIN}"
mv -f "${TMPBIN}" /usr/local/bin/mon-agent
trap - EXIT

# 5. config (root:monagent 0640)
cat >/etc/mon-agent/config.yaml <<EOF
server_url: ${SERVER_URL}
key_file: /var/lib/mon-agent/agent.key
buffer_dir: /var/lib/mon-agent
buffer_max_mb: 100
labels: {}
packages:
  enabled: true
redact:
  enabled: false
EOF
chown root:monagent /etc/mon-agent/config.yaml
chmod 0640 /etc/mon-agent/config.yaml

# 6. systemd unit
cat >/etc/systemd/system/mon-agent.service <<'EOF'
[Unit]
Description=mon agent (read-only host monitor)
After=network-online.target
Wants=network-online.target

[Service]
Type=simple
User=monagent
Group=monagent
ExecStart=/usr/local/bin/mon-agent --config=/etc/mon-agent/config.yaml
Restart=on-failure
RestartSec=5
NoNewPrivileges=yes
ProtectSystem=strict
ProtectHome=yes
ReadWritePaths=/var/lib/mon-agent /var/log/mon-agent
PrivateTmp=yes

[Install]
WantedBy=multi-user.target
EOF
chmod 0644 /etc/systemd/system/mon-agent.service

systemctl daemon-reload

# 7. one-shot bootstrap (only if no key file yet) — consumes the token.
# mon-agent does not exit after a successful bootstrap; it continues into
# its main loop. Wrap the call in coreutils timeout so this installer can
# hand control back to systemd. Exit code 124 is the timeout fired AFTER
# the key file was written, which is exactly the success path here.
if [ ! -f /var/lib/mon-agent/agent.key ]; then
  timeout 8 sudo -u monagent /usr/local/bin/mon-agent \
    --config=/etc/mon-agent/config.yaml \
    --bootstrap-token="${BOOTSTRAP_TOKEN}" || true
fi
[ -f /var/lib/mon-agent/agent.key ] || { echo "BOOTSTRAP FAILED" >&2 ; exit 1 ; }

# 8. enable + start
systemctl enable --now mon-agent.service

# 9. self-update timer — checks the server every 6h, sha256-verifies the
# fetched binary, atomic-replaces, and restarts mon-agent. The agent's
# main loop never updates itself; it's always the timer-driven oneshot.
cat >/etc/systemd/system/mon-agent-update.service <<'EOF'
[Unit]
Description=mon agent self-updater (one-shot, root)
Documentation=https://github.com/MalteKiefer/MonSys
ConditionPathExists=/usr/local/bin/mon-agent
After=network-online.target mon-agent.service
Wants=network-online.target

[Service]
Type=oneshot
User=root
Group=root
ExecStart=/usr/local/bin/mon-agent --self-update --config=/etc/mon-agent/config.yaml

ProtectSystem=strict
ReadWritePaths=/usr/local/bin /var/lib/mon-agent /run/systemd
ProtectHome=yes
PrivateTmp=yes
PrivateDevices=yes
ProtectHostname=yes
ProtectClock=yes
ProtectKernelTunables=yes
ProtectKernelModules=yes
ProtectKernelLogs=yes
ProtectControlGroups=yes
ProtectProc=invisible
ProcSubset=pid

NoNewPrivileges=yes
LockPersonality=yes
RestrictNamespaces=yes
RestrictRealtime=yes
RestrictSUIDSGID=yes
RestrictAddressFamilies=AF_INET AF_INET6 AF_UNIX
SystemCallArchitectures=native
SystemCallFilter=@system-service
SystemCallFilter=~@privileged @resources @mount @swap @reboot @raw-io @debug @cpu-emulation @obsolete

UMask=0022
TimeoutStartSec=5min
EOF
chmod 0644 /etc/systemd/system/mon-agent-update.service

cat >/etc/systemd/system/mon-agent-update.timer <<'EOF'
[Unit]
Description=mon agent self-update timer
Documentation=https://github.com/MalteKiefer/MonSys

[Timer]
OnBootSec=5min
OnUnitActiveSec=6h
RandomizedDelaySec=15min
Persistent=true
Unit=mon-agent-update.service

[Install]
WantedBy=timers.target
EOF
chmod 0644 /etc/systemd/system/mon-agent-update.timer
systemctl daemon-reload
systemctl enable --now mon-agent-update.timer

echo "mon-agent installed and started; self-update timer enabled."
`
