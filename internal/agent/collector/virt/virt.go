// Package virt detects KVM (libvirt) and system-LXC machines on the host.
//
// libvirt is preferred over driver-specific tooling (qemu-system-* / lxc-info)
// because it covers KVM, libvirt-LXC, and Xen with a single CLI surface and
// reports authoritative state regardless of the underlying driver.
//
// All commands are read-only. The collector degrades silently if a tool is
// unavailable so a host without KVM/LXC simply contributes nothing.
package virt

import (
	"bufio"
	"bytes"
	"context"
	"strconv"
	"strings"
	"time"

	"github.com/MalteKiefer/MonSys/internal/agent/safeexec"
	"github.com/MalteKiefer/MonSys/internal/shared/apitypes"
)

type Collector struct {
	hasVirsh   bool
	hasLxc     bool
	hasProxmox bool
}

func New() *Collector {
	return &Collector{
		hasVirsh:   safeexec.Available("virsh"),
		hasLxc:     safeexec.Available("lxc-ls"),
		hasProxmox: proxmoxAvailable(),
	}
}

func (c *Collector) Name() string { return "virt" }

// Available is true when at least one virtualization manager is reachable.
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
		}
	}
	if c.hasLxc {
		if vms, err := c.systemLXC(ctx); err == nil {
			collected = append(collected, vms...)
		}
	}
	if c.hasProxmox {
		if vms, err := c.proxmoxVMs(ctx); err == nil {
			collected = append(collected, vms...)
		}
		if vms, err := c.proxmoxLXC(ctx); err == nil {
			collected = append(collected, vms...)
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

func (c *Collector) libvirtDomains(ctx context.Context) ([]apitypes.VMInfo, error) {
	out, err := safeexec.RunWithTimeout(ctx, 8*time.Second, "virsh", "list", "--all", "--name")
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
			continue
		}
		vms = append(vms, info)
	}
	return vms, sc.Err()
}

func virshDominfo(ctx context.Context, name string) (apitypes.VMInfo, error) {
	out, err := safeexec.RunWithTimeout(ctx, 5*time.Second, "virsh", "dominfo", name)
	if err != nil {
		return apitypes.VMInfo{}, err
	}
	v := apitypes.VMInfo{Name: name, Kind: "kvm"}
	for _, line := range strings.Split(string(out), "\n") {
		k, val, ok := strings.Cut(line, ":")
		if !ok {
			continue
		}
		val = strings.TrimSpace(val)
		switch strings.TrimSpace(k) {
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
			// "8388608 KiB" → bytes
			fields := strings.Fields(val)
			if len(fields) >= 1 {
				if n, err := strconv.ParseInt(fields[0], 10, 64); err == nil {
					unit := "KiB"
					if len(fields) >= 2 {
						unit = fields[1]
					}
					v.MemBytes = n * unitBytes(unit)
				}
			}
		case "Autostart":
			v.Autostart = strings.EqualFold(val, "enable")
		}
	}
	if v.ExternalID == "" {
		// Fall back to name when libvirt didn't return a UUID (rare; usually a parsing issue).
		v.ExternalID = name
	}
	return v, nil
}

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

func (c *Collector) systemLXC(ctx context.Context) ([]apitypes.VMInfo, error) {
	// `lxc-ls -f` produces a header + columns. We use --fancy-format with a
	// fixed column list so column order is stable across distributions.
	out, err := safeexec.RunWithTimeout(ctx, 5*time.Second,
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

func parseInt0(s string) int {
	n, err := strconv.Atoi(strings.TrimSpace(s))
	if err != nil {
		return 0
	}
	return n
}

