//go:build linux

package mail

import (
	"net/http"
	"time"

	"github.com/MalteKiefer/MonSys/internal/agent/config"
	"github.com/MalteKiefer/MonSys/internal/agent/safeexec"
)

// New returns a production Collector wired to safeexec.Run, the standard
// postfix spool directory, and the rspamd stat URL from cfg. The constructor
// lives in a linux-tagged file because both config and safeexec carry linux
// build constraints; Collector and Collect in mail.go remain OS-agnostic.
func New(cfg config.Config) *Collector {
	return &Collector{
		exec:      safeexec.Run,
		spoolRoot: "/var/spool/postfix",
		httpClient: &http.Client{
			Timeout: 5 * time.Second,
		},
		statURL:  cfg.RspamdStatURL(),
		dialHost: "127.0.0.1",
	}
}
