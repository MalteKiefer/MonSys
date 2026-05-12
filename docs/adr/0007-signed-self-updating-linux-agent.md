# ADR-0007: Signed self-updating Linux agent

- Status: Accepted
- Date: 2026-05-12
- Deciders: maintainers
- Context tags: agent, security, build, platform

## Context and Problem Statement

`mon-agent` runs on every monitored host. The monitored estate
includes long-tail VMs and bare metal where "I will SSH in and `apt
upgrade` the agent" is wishful thinking. We need:

- A self-update mechanism that the agent can drive itself.
- Tamper-evident updates: the server is *not* implicitly trusted to
  ship the next binary; the agent has to verify a signature before
  swapping.
- Atomic replacement of a running ELF.
- A rollback path if the new binary fails to start.
- A signed-release pipeline that fits CI (no offline signing
  ceremony every release).
- A clear story for "what does the Windows port cost?" — operators
  have asked.

Forces:

- GPG signing is the obvious choice if your contributors already
  have a key. It comes with a keyring, a trust-DB, web-of-trust
  noise, and a CLI surface (`gpg --verify`) that the agent would
  need to embed or shell out to.
- minisign (https://jedisct1.github.io/minisign/) is a 1-file
  Ed25519 signer with a tiny verifier. No keyring. Smaller code
  surface. No "pubkey expired in 2031" footgun.
- Replacing a running ELF on Linux is fine: `rename(2)` over the
  open executable is allowed because the kernel keeps the
  underlying inode alive until the running process exits
  (`exec_mmap` in `fs/binfmt_elf.c`). On Windows, the same trick
  raises `ERROR_SHARING_VIOLATION`.
- We want a "previous binary" snapshot for rollback, but we don't
  want it loitering forever and we don't want it to be writable
  by anyone but root.

## Considered Options

1. **GPG-signed updates.** Industry standard. Big tooling surface,
   keyring management, and a verifier that pulls in libgcrypt.
2. **minisign-signed updates.** Ed25519, single key file, single
   binary verifier, no keyring. Embeddable in 200 LOC.
3. **TLS-only trust** ("the server is trusted because it's
   pinned"). Cheaper but means a compromised server can push
   arbitrary code to every agent. Hard no.
4. **APT/DEB + DNF/RPM-only delivery.** Operator-friendly but
   stranded distros (Alpine? Arch? Rocky 9? Containerised
   appliances?) and the agent can't drive its own updates without
   admin-level package-manager privileges anyway.
5. **Cross-platform from day one (Windows + Linux).** Doubles the
   surface. Estimate (see below): ~15 person-days, with painful
   pieces in service management, ELF→PE atomic-replace, and the
   privilege model.

## Decision Outcome

Chosen: **option 2 + Linux-only today** — minisign-signed binaries
fetched by the agent, atomic rename-over-running-exe, `mon-agent.prev`
snapshot for rollback, Linux-only build with explicit build tags
ready for a future Windows port.

Rationale:

- **minisign over GPG: smaller attack surface and zero key-management
  ceremony.** Release CI generates a detached `.minisig` next to each
  binary; the agent embeds the public key at compile time and verifies
  using a small Ed25519 verifier. No keyring file on disk, no trust-
  DB, no expiry. The minisign install on the release runner is
  pinned to upstream v0.11 via SHA256-pinned tarball download (not
  `apt-get install minisign`, which we replaced in `91550d0`'s
  F-4.3.1.14 finding) so a compromised apt mirror can't substitute a
  malicious signer.
- **Atomic rename-over-running-exe relies on Linux `exec_mmap`.** The
  updater writes the new binary to a temp file in the install
  directory, fsyncs, then `rename(2)`s over the current executable.
  The kernel's `exec_mmap` semantics keep the old inode alive for
  the running process; the next start picks up the new inode. EXDEV
  (cross-filesystem) falls back to `copyReplace` — see `14a5bb6`
  F-4.3.7 for the rollback-path mirror of the same fallback.
- **`prev` rollback snapshot.** Before the swap, the updater copies
  the current binary to `mon-agent.prev`. If the new binary fails
  to start (systemd `RestartSec` window), the rollback branch
  renames `prev` back into place. On happy path, `prev` is removed
  to avoid loiter. The rollback branch intentionally *preserves* the
  `prev` file so an operator can roll forward manually — that's the
  `14a5bb6` F-4.3.6 fix.
- **Spool atomicity (`14a5bb6`).** The agent buffers events to a
  spool dir when offline. The tmp file opens with `O_EXCL` so a
  collision between two agent processes fails loudly. `buffer.Append`
  fsyncs the spool directory after the atomic rename so the entry
  is durable on ext4 with non-default mount options.
- **Permanent-network-error detection.** `isPermanentNetError`
  recognises `*tls.CertificateVerificationError`,
  `x509.UnknownAuthorityError`, `x509.CertificateInvalidError`,
  `x509.HostnameError`, and `net.DNSError` with `IsNotFound`. The
  transport refuses to retry these — three retries on a cert pin
  mismatch are wasted handshakes (`14a5bb6` F-4.3.11).
- **Linux-only today, deliberate.** Build tags (`//go:build linux`)
  on every file that reads `/proc`, `/sys`, `/etc`, or shells
  distro-specific binaries. Sibling stub files (`virt/virt_stub.go`,
  `workload/doc_other.go`) keep packages non-empty so cross-compile
  to `GOOS=windows` is a clean no-op rather than a "no Go files"
  error.

**Windows port cost estimate (~15 person-days):**

- Service management: rewrite the systemd unit + timer install
  flow as a Windows Service / Scheduled Task. Distinct security
  context model (LocalService vs root) + per-user vs LocalMachine
  scope.
- Atomic replace: no `rename-over-running-exe` on Windows; the
  standard approach is `MoveFileEx` with
  `MOVEFILE_DELAY_UNTIL_REBOOT` *or* the `.old`-stash + scheduled
  swap. Adds a "needs restart" state.
- Privilege escalation: `pkexec` is Linux-only; Windows uses UAC +
  manifest. The install/uninstall flow has to drop a one-time
  elevated installer.
- Collector parity: every `/proc`-based collector needs a WMI /
  PerfCounter / WinPSAPI replacement. Some (NIC link state, disk
  utilisation) are straightforward; firewall state (`netsh
  advfirewall`), fail2ban (no equivalent), CrowdSec (yes — they
  ship a Windows agent), workload (Docker Desktop only) get
  thornier.
- Self-update transport stays the same (minisign verifies on
  Windows too) — the swap mechanism is the painful piece.

Net: a Windows port is *possible* but is a separate decision
point. Today we ship Linux-only, with the codebase shaped so the
port is mechanical, not architectural.

### Consequences

- Positive:
  - Tamper-evident updates without GPG infrastructure.
  - Atomic, in-place upgrade with rollback.
  - Spool durability against power-loss after the F-4.3.13 fsync
    fix.
  - Build tags ready for the Windows port.
- Negative:
  - The signing key is in CI's hands. If a CI compromise lets an
    attacker sign a malicious binary, every agent self-updates to
    it. Mitigation: workflow concurrency control (`91550d0`
    F-4.3.1.17), permissions scoped per-job, minisign install
    SHA-pinned.
  - Linux-only today. Operators on Windows servers see "the
    monitor doesn't support our estate" and we have to point at
    this ADR.
  - The `prev` snapshot doubles the disk footprint of the agent
    binary on happy-path between updates (briefly) and forever in
    the rollback branch (intentionally).
- Follow-ups:
  - Windows port (out of scope; ~15 person-days estimate).
  - Sigstore / cosign keyless on the binaries too (we already do
    it on the container image — see ADR-0009).

## More Information

- Implementation commits:
  - `ad2e597` refactor(agent): build tags, slog components, split
    per-manager files, doc surface — Linux build tags +
    Windows-ready stubs.
  - `14a5bb6` security(agent): updater hygiene, retry semantics,
    spool atomicity — F-4.3.6 `prev` cleanup,
    F-4.3.7 rollback EXDEV fallback, F-4.3.11
    permanent-net-error detection, F-4.3.13 fsync spool dir,
    F-4.3.15 `O_EXCL` on tmp file.

- References:
  - minisign: https://jedisct1.github.io/minisign/
  - `exec_mmap` semantics: `fs/binfmt_elf.c` and `man 2 rename`.
  - OWASP A08:2025 "Software & Data Integrity Failures" — signed
    update channels.
  - SLSA Level 3 — provenance + isolated build, partially
    addressed by ADR-0009.

- Related: ADR-0009 (signed container image — the other half of
  "everything we ship is verifiable"), ADR-0010 (the agent
  participates in the auth surface via enrollment tokens).
