//go:build linux

package mail

import (
	"net/http"
	"os"
	"time"

	"github.com/MalteKiefer/MonSys/internal/agent/config"
	"github.com/MalteKiefer/MonSys/internal/agent/safeexec"
)

// New returns a production Collector wired to safeexec.Run, the standard
// postfix spool directory, and the rspamd stat URL from cfg. The constructor
// lives in a linux-tagged file because both config and safeexec carry linux
// build constraints; Collector and Collect in mail.go remain OS-agnostic.
func New(cfg config.Config) *Collector {
	// serverName drives TLS SNI + cert verification on the probed ports. Mail
	// certs are issued for the public FQDN, so we verify against the host's
	// hostname while dialing loopback. os.Hostname is the host's configured
	// name (an FQDN on a typical mail host); on mismatch the probe degrades to
	// CertTrusted:false rather than failing.
	serverName, _ := os.Hostname()
	return &Collector{
		exec:      safeexec.Run,
		spoolRoot: "/var/spool/postfix",
		httpClient: &http.Client{
			Timeout: 5 * time.Second,
		},
		statURL:    cfg.RspamdStatURL(),
		dialHost:   "127.0.0.1",
		serverName: serverName,
	}
}
