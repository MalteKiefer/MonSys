# Privacy & Data Inventory

Operator-facing record of what `mon-agent` collects, why, and what
obligations come with it. Pairs with audit finding AUDIT-405 and is
written so it can be lifted directly into a GDPR Article 30 record of
processing activities.

## Scope

`mon-agent` runs as a root-equivalent systemd service on every
monitored Linux host. It pushes batched payloads to `mon-server` over
HTTPS (mTLS-equivalent via per-agent keys). Nothing else egresses.
The web SPA and TimescaleDB are server-side; this document only
catalogues what the agent **produces**, because that is the only place
where personal data enters the system.

## Data inventory by collector

| Collector  | Field(s)                                                                                       | Personal data? | Disable via                                       |
|------------|------------------------------------------------------------------------------------------------|----------------|---------------------------------------------------|
| System     | cpu %, ram, load average, uptime                                                               | No             | n/a (core metric)                                 |
| Disks      | mountpoint, fstype, size, usage                                                                | No             | n/a                                               |
| NICs       | interface name, mac, ipv4/ipv6, rx/tx counters                                                 | No (host-attributed) | n/a                                         |
| Workloads  | container/service id, image, state                                                             | No             | n/a                                               |
| VMs        | proxmox vmid, name, status, owner-token-id (no human users)                                    | No             | `proxmox.enabled: false`                          |
| Inventory  | hostname, machine-id, distro, kernel, arch                                                     | Indirect       | n/a (core)                                        |
| Packages   | name, version, manager (apt/dnf/apk/pacman), repo                                              | No             | `packages.enabled: false`                         |
| ObservedUser | username, uid, gid, login shell, home directory, last_login_at, is_sudoer, is_system          | **Yes**        | `redact.enabled` + `redact.shells` + `redact.homes` |
| LoginEvent | timestamp, username, source_ip, auth method (password/key/pam-…), success bool                | **Yes**        | `redact.source_ips` (hashes IPs); to suppress entirely, mask via auditd config |
| Security/firewalls | rule snapshot excerpts (nftables/iptables/ufw/firewalld)                                | Indirect       | n/a (security telemetry)                          |
| Security/fail2ban | jail names, currently banned IPs                                                         | **Yes** (IPs)  | uninstall fail2ban or stop the agent's f2b probe  |
| Security/crowdsec | active decisions: scope, value (often IP/range), origin, scenario                        | **Yes** (IPs)  | uninstall crowdsec or stop the agent's cs probe   |

"Personal data" follows the GDPR Art. 4(1) definition: any information
relating to an identified or identifiable natural person. A username
without further context is enough to qualify.

## GDPR Article 30 mapping

| Field                              | Value                                                                                                  |
|------------------------------------|--------------------------------------------------------------------------------------------------------|
| Controller                         | The operator running the `mon` deployment.                                                             |
| Processor                          | None by default. `mon` is self-hosted; if you outsource hosting, the hosting provider is your processor. |
| Purpose of processing              | Infrastructure security monitoring, change auditing, intrusion detection.                              |
| Lawful basis                       | Art. 6(1)(f) — legitimate interest in protecting the operator's information systems.                   |
| Categories of data subjects        | (a) Employees / contractors with shell accounts on monitored hosts. (b) External actors generating login attempts or matched by fail2ban / crowdsec. |
| Categories of personal data        | Account identifiers (username, uid), technical identifiers (source IP), authentication outcomes.       |
| Categories of recipients           | Operator's own admins via the web UI; no third-party sharing.                                          |
| Third-country transfers            | None unless the operator deploys `mon-server` outside the EEA.                                         |
| Retention                          | See "Retention" below.                                                                                 |
| Technical & organisational measures | mTLS-equivalent ingest, agent runs as `monagent` non-root user with systemd hardening, TLS at the edge, role-based UI auth, audit log, optional agent-side redaction. |

## Retention

`mon-server` reaper job enforces:

- **Metrics hypertables** (`metrics_*`): 30 days.
- **Login events, alert history, audit log**: 90 days.

Inventory snapshots (users, packages, security state) are kept as the
**latest** observation per host; superseded snapshots are dropped on
the next push. Operators who need shorter windows can lower the policy
on the relevant TimescaleDB hypertables; a longer window is permitted
only where the operator has documented the legitimate interest.

## Operator obligations

1. **Purpose limitation.** Do not query `mon` data for HR investigations
   or productivity monitoring. The lawful basis above is security only;
   re-purposing requires a fresh basis (consent, contract, etc.).
2. **Access control.** Restrict the `admin` and `viewer` roles to staff
   who need the data for their job. Audit access via the audit log.
3. **Inform data subjects.** Employees with shell accounts on monitored
   hosts must know their logins, sudo state, and source IPs are
   recorded. Add this to your acceptable-use policy or onboarding.
4. **Data subject requests.** Personal data in `mon` is keyed by
   `(host_id, username)` for users and `(host_id, source_ip)` for
   login events. Both are searchable; export and erasure can be
   performed via direct SQL against the hypertables.
5. **Minimise on the wire.** Operators in regulated environments should
   enable the agent-side redactors before payloads leave the host:

   ```yaml
   # /etc/mon-agent/config.yaml
   redact:
     enabled: true
     shells: true       # mask shell paths in observed users
     homes: true        # mask home directories
     source_ips: true   # hash source_ip in login events (sha256, first 8 hex)
   ```

## Disabling PII-heavy collectors

If your deployment doesn't need a given source, turn it off rather than
collecting and redacting:

| Collector              | Toggle                                |
|------------------------|---------------------------------------|
| Package inventory      | `packages.enabled: false`             |
| Proxmox VM inventory   | `proxmox.enabled: false`              |
| fail2ban / crowdsec    | Uninstall or stop the local service; the agent skips probes that aren't present. |
| LoginEvent ingestion   | Mask via auditd / wtmp configuration on the host; the agent reads what the OS exposes. |

Defaults are conservative: only the system-metric and inventory
collectors are required for `mon` to work. Everything else can be
turned off without breaking the dashboard.
