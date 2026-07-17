// Package mail implements the mail-stack collector. It probes the local system
// for postfix, dovecot, rspamd, and postgrey via systemd, then assembles a
// MailReport only when at least one of those units is present. This gating
// means hosts that run no mail services produce no Mail payload rather than an
// empty report full of zeroes.
//
// All external interactions (systemd queries, HTTP calls, TCP dials) are
// injected via struct fields so unit tests can run without root or a real
// mail stack.
//
// The constructor New is defined in mail_linux.go (linux-only) because it
// imports config and safeexec, both of which carry linux build constraints.
// This file and Collect are OS-agnostic so the package tests compile on darwin.
package mail

import (
	"context"
	"net/http"
	"time"

	"github.com/MalteKiefer/MonSys/internal/shared/apitypes"
)

// units lists the mail-related systemd service units that the collector probes.
// Order matters only for readability of the resulting Services slice.
var units = []string{
	"postfix.service",
	"dovecot.service",
	"rspamd.service",
	"postgrey.service",
}

// postfixPorts lists the TCP ports that the collector checks when postfix is
// present. 465 (implicit-TLS submissions) is included alongside 25/587 because
// many deployments use implicit TLS instead of STARTTLS on 587.
var postfixPorts = []portSpec{
	{Port: 25, Proto: "smtp", TLS: false},
	{Port: 465, Proto: "submissions", TLS: true},
	{Port: 587, Proto: "submission", TLS: false},
}

// dovecotPorts lists the TCP ports that the collector checks when dovecot is
// present, covering both IMAP and POP3 in plain + implicit-TLS variants.
var dovecotPorts = []portSpec{
	{Port: 143, Proto: "imap", TLS: false},
	{Port: 993, Proto: "imaps", TLS: true},
	{Port: 995, Proto: "pop3s", TLS: true},
}

// Collector implements collector.Source for the mail stack. Its fields are all
// injectable so tests can drive every code path without spawning real processes.
type Collector struct {
	exec       execFn
	spoolRoot  string
	httpClient *http.Client
	statURL    string
	dialHost   string
	// serverName is the FQDN used for TLS SNI + cert verification on the
	// probed ports (certs are issued for the public hostname, not 127.0.0.1).
	serverName string
}

// Name returns the collector identifier used in logs and the inventory sources list.
func (c *Collector) Name() string { return "mail" }

// Collect probes postfix, dovecot, rspamd, and postgrey via systemd. If none
// of the units are present on this host, batch.Mail is left nil and nil is
// returned — the host simply doesn't run a mail stack. Otherwise a MailReport
// is assembled with only the components that are actually present, and
// batch.Mail is set to point at it.
func (c *Collector) Collect(ctx context.Context, batch *apitypes.IngestRequest) error {
	type unitState struct {
		present bool
		active  bool
		sub     string
	}

	states := make(map[string]unitState, len(units))
	anyPresent := false

	for _, unit := range units {
		present, active, sub := serviceState(ctx, c.exec, unit)
		states[unit] = unitState{present: present, active: active, sub: sub}
		if present {
			anyPresent = true
		}
	}

	// If no mail unit is installed on this host, emit no report.
	if !anyPresent {
		return nil
	}

	report := apitypes.MailReport{
		Time: time.Now(),
	}

	// Populate Services for every present unit.
	for _, unit := range units {
		s := states[unit]
		if !s.present {
			continue
		}
		// Strip the ".service" suffix for the display name.
		name := unit
		if len(name) > 8 && name[len(name)-8:] == ".service" {
			name = name[:len(name)-8]
		}
		report.Services = append(report.Services, apitypes.MailService{
			Name:     name,
			Active:   s.active,
			SubState: s.sub,
		})
	}

	// Queue is only relevant when postfix is installed.
	if states["postfix.service"].present {
		report.Queue = postfixQueue(c.spoolRoot)
	}

	// Rspamd stat is only fetched when rspamd is installed.
	if states["rspamd.service"].present {
		report.Rspamd = rspamdStat(ctx, c.httpClient, c.statURL)
	}

	// Port checks are scoped to the detected services.
	var ports []apitypes.MailPortCheck
	if states["postfix.service"].present {
		for _, spec := range postfixPorts {
			ports = append(ports, checkPort(ctx, c.dialHost, c.serverName, spec))
		}
	}
	if states["dovecot.service"].present {
		for _, spec := range dovecotPorts {
			ports = append(ports, checkPort(ctx, c.dialHost, c.serverName, spec))
		}
	}
	report.Ports = ports

	batch.Mail = &report
	return nil
}
