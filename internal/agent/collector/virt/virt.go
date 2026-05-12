//go:build linux

// Package virt detects KVM (libvirt) and system-LXC machines on the host,
// plus Proxmox VE-managed VMs and containers.
//
// libvirt is preferred over driver-specific tooling (qemu-system-* / lxc-info)
// because it covers KVM, libvirt-LXC, and Xen with a single CLI surface and
// reports authoritative state regardless of the underlying driver. Proxmox VE
// has its own stack (qm/pct/etc/pve) and is queried separately when present.
//
// All commands are read-only. The collector degrades silently if a tool is
// unavailable so a host without KVM/LXC simply contributes nothing.
//
// This package is Linux-only: every backend shells to a Linux-specific binary
// (virsh, lxc-ls, qm, pct) or reads /etc/pve. A non-Linux build receives a
// stub (virt_stub.go) so the agent package still compiles on other platforms.
package virt

import (
	"bufio"
	"bytes"
	"context"
	"log/slog"
	"strconv"
	"strings"
	"time"

	"github.com/MalteKiefer/MonSys/internal/agent/safeexec"
	"github.com/MalteKiefer/MonSys/internal/shared/apitypes"
)

// Tool timeouts. Sized for the slowest realistic case on a busy hypervisor:
// virsh list can stall on a hung qemu monitor; qm/pct may block briefly on
// /etc/pve corosync I/O. Five-second values match other collectors.
const (
	virshListTimeout    = 8 * time.Second
	virshDominfoTimeout = 5 * time.Second
	lxcLsTimeout        = 5 * time.Second
	qmListTimeout       = 8 * time.Second
	pctListTimeout      = 8 * time.Second
)

// logger is the package-scoped slog handle. All log lines emitted from this
// collector carry component=virt so operators can grep a single tag across
// libvirt/LXC/Proxmox paths.
var logger = slog.With("component", "virt")

// Collector probes the host for virtualization backends at construction time
// and contributes VMInfo entries to the inventory snapshot.
//
// One Collector is reused for the lifetime of the agent; the probe results
// (hasVirsh / hasLxc / hasProxmox) are cached so a missing tool does not
// cost a PATH lookup per tick.
type Collector struct {
	hasVirsh   bool
	hasLxc     bool
	hasProxmox bool
}

// New constructs a Collector and probes for available virtualization tooling.
// The probe is best-effort: missing binaries are not an error, the
// corresponding backend is simply disabled.
func New() *Collector {
	return &Collector{
		hasVirsh:   safeexec.Available("virsh"),
		hasLxc:     safeexec.Available("lxc-ls"),
		hasProxmox: proxmoxAvailable(),
	}
}

// Name returns the collector identifier used in registry and log fields.
func (c *Collector) Name() string { return "virt" }

// Available reports whether at least one virtualization manager is reachable.
// When false the agent skips registering this collector entirely.
func (c *Collector) Available() bool { return c.hasVirsh || c.hasLxc || c.hasProxmox }

// Inventory contributes VMInfo entries to the snapshot. We do not emit
// time-series samples for VMs in this iteration — host-level CPU/RAM already
// captures the resource cost; per-domain stats can come later via virsh dominfo.
//
// Sources are merged and de-duplicated by (Kind, ExternalID): a Proxmox host
// commonly runs libvirt as well, so the same KVM domain can otherwise appear
// twice (once via virsh and once via qm).
func (c *Collector) Inventory(ctx context.Context, snap *apitypes.InventorySnap) error {
	var collected []apitypes.VMInfo
	if c.hasVirsh {
		if vms, err := c.libvirtDomains(ctx); err == nil {
			collected = append(collected, vms...)
		} else {
			logger.Debug("libvirt enumeration failed", "err", err)
		}
	}
	if c.hasLxc {
		if vms, err := c.systemLXC(ctx); err == nil {
			collected = append(collected, vms...)
		} else {
			logger.Debug("system-lxc enumeration failed", "err", err)
		}
	}
	if c.hasProxmox {
		if vms, err := c.proxmoxVMs(ctx); err == nil {
			collected = append(collected, vms...)
		} else {
			logger.Debug("proxmox qm list failed", "err", err)
		}
		if vms, err := c.proxmoxLXC(ctx); err == nil {
			collected = append(collected, vms...)
		} else {
			logger.Debug("proxmox pct list failed", "err", err)
		}
	}
	snap.VMs = append(snap.VMs, dedupVMs(collected)...)
	return nil
}

// dedupVMs removes duplicate entries by (Kind, ExternalID), preserving the
// first occurrence. Order matters: callers populate libvirt/system-LXC first
// so libvirt's UUID-based ExternalID wins over the numeric Proxmox VMID when
// both stacks see the same domain. (They have different ExternalID shapes,
// so a "duplicate" really only occurs when the Proxmox path is the only one
// returning a given VMID — which is the common case.)
func dedupVMs(in []apitypes.VMInfo) []apitypes.VMInfo {
	if len(in) == 0 {
		return in
	}
	seen := make(map[string]struct{}, len(in))
	out := make([]apitypes.VMInfo, 0, len(in))
	for _, v := range in {
		key := v.Kind + "\x00" + v.ExternalID
		if _, dup := seen[key]; dup {
			continue
		}
		seen[key] = struct{}{}
		out = append(out, v)
	}
	return out
}

// Collect is a no-op: VM time-series belong in a follow-up milestone.
func (c *Collector) Collect(_ context.Context, _ *apitypes.IngestRequest) error { return nil }

// --- libvirt ---------------------------------------------------------------

// libvirtDomains lists every libvirt domain (running or not) and enriches
// each entry with details from `virsh dominfo`. Errors on individual
// dominfo calls are logged at debug and the domain is skipped.
func (c *Collector) libvirtDomains(ctx context.Context) ([]apitypes.VMInfo, error) {
	out, err := safeexec.RunWithTimeout(ctx, virshListTimeout, "virsh", "list", "--all", "--name")
	if err != nil {
		return nil, err
	}
	var vms []apitypes.VMInfo
	sc := bufio.NewScanner(bytes.NewReader(out))
	for sc.Scan() {
		name := strings.TrimSpace(sc.Text())
		if name == "" {
			continue
		}
		info, err := virshDominfo(ctx, name)
		if err != nil {
			logger.Debug("virsh dominfo failed", "domain", name, "err", err)
			continue
		}
		vms = append(vms, info)
	}
	return vms, sc.Err()
}

// virshDominfo runs `virsh dominfo NAME` and maps the key/value output to a
// VMInfo. Unknown keys are ignored; a missing UUID falls back to the name
// so ExternalID is always populated.
func virshDominfo(ctx context.Context, name string) (apitypes.VMInfo, error) {
	out, err := safeexec.RunWithTimeout(ctx, virshDominfoTimeout, "virsh", "dominfo", name)
	if err != nil {
		return apitypes.VMInfo{}, err
	}
	v := apitypes.VMInfo{Name: name, Kind: "kvm"}
	for _, line := range strings.Split(string(out), "\n") {
		k, val, ok := strings.Cut(line, ":")
		if !ok {
			continue
		}
		applyDominfoField(&v, strings.TrimSpace(k), strings.TrimSpace(val))
	}
	if v.ExternalID == "" {
		// Fall back to name when libvirt didn't return a UUID (rare; usually a parsing issue).
		v.ExternalID = name
	}
	return v, nil
}

// applyDominfoField writes one parsed `virsh dominfo` key/value pair onto v.
// Extracted from virshDominfo to keep the parser flat and per-field testable.
func applyDominfoField(v *apitypes.VMInfo, key, val string) {
	switch key {
	case "UUID":
		v.ExternalID = val
	case "OS Type":
		if strings.EqualFold(val, "exe") {
			v.Kind = "libvirt-lxc"
		}
	case "State":
		v.State = val
	case "CPU(s)":
		v.VCPU, _ = strconv.Atoi(val)
	case "Max memory":
		// "8388608 KiB" → bytes.
		fields := strings.Fields(val)
		if len(fields) == 0 {
			return
		}
		n, err := strconv.ParseInt(fields[0], 10, 64)
		if err != nil {
			return
		}
		unit := "KiB"
		if len(fields) >= 2 {
			unit = fields[1]
		}
		v.MemBytes = n * unitBytes(unit)
	case "Autostart":
		v.Autostart = strings.EqualFold(val, "enable")
	}
}

// unitBytes maps a libvirt memory unit suffix (KiB/MiB/GiB/KB/MB/GB/bytes)
// to its byte multiplier. Unknown units fall back to 1024, libvirt's
// historical default for "Max memory".
func unitBytes(u string) int64 {
	switch strings.ToLower(strings.TrimSpace(u)) {
	case "b", "bytes":
		return 1
	case "kib":
		return 1024
	case "mib":
		return 1024 * 1024
	case "gib":
		return 1024 * 1024 * 1024
	case "kb":
		return 1000
	case "mb":
		return 1000 * 1000
	case "gb":
		return 1000 * 1000 * 1000
	}
	return 1024 // libvirt's historical default for "Max memory"
}

// --- system LXC ------------------------------------------------------------

// systemLXC lists containers managed by the system LXC stack (i.e. NOT
// Proxmox's pve-container, which is handled in proxmox.go). The fancy-format
// column list is fixed so column order is stable across distributions.
func (c *Collector) systemLXC(ctx context.Context) ([]apitypes.VMInfo, error) {
	out, err := safeexec.RunWithTimeout(ctx, lxcLsTimeout,
		"lxc-ls", "--fancy", "--fancy-format", "name,state,autostart")
	if err != nil {
		return nil, err
	}
	var vms []apitypes.VMInfo
	sc := bufio.NewScanner(bytes.NewReader(out))
	first := true
	for sc.Scan() {
		// Skip header line.
		if first {
			first = false
			continue
		}
		f := strings.Fields(sc.Text())
		if len(f) < 2 {
			continue
		}
		v := apitypes.VMInfo{
			Kind:       "lxc",
			Name:       f[0],
			ExternalID: f[0],
			State:      f[1],
		}
		if len(f) >= 3 {
			v.Autostart = parseInt0(f[2]) == 1
		}
		vms = append(vms, v)
	}
	return vms, sc.Err()
}

// parseInt0 returns the int value of s or 0 if s is not a valid integer.
// Used for the "autostart" column of `lxc-ls --fancy`, where 0/1 is the
// stable representation across LXC versions.
func parseInt0(s string) int {
	n, err := strconv.Atoi(strings.TrimSpace(s))
	if err != nil {
		return 0
	}
	return n
}
