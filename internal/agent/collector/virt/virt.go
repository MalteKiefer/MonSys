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

	"github.com/pr0ph37/mon/internal/agent/safeexec"
	"github.com/pr0ph37/mon/internal/shared/apitypes"
)

type Collector struct {
	hasVirsh bool
	hasLxc   bool
}

func New() *Collector {
	return &Collector{
		hasVirsh: safeexec.Available("virsh"),
		hasLxc:   safeexec.Available("lxc-ls"),
	}
}

func (c *Collector) Name() string { return "virt" }

// Available is true when at least one virtualization manager is reachable.
func (c *Collector) Available() bool { return c.hasVirsh || c.hasLxc }

// Inventory contributes VMInfo entries to the snapshot. We do not emit
// time-series samples for VMs in this iteration — host-level CPU/RAM already
// captures the resource cost; per-domain stats can come later via virsh dominfo.
func (c *Collector) Inventory(ctx context.Context, snap *apitypes.InventorySnap) error {
	if c.hasVirsh {
		vms, err := c.libvirtDomains(ctx)
		if err == nil {
			snap.VMs = append(snap.VMs, vms...)
		}
	}
	if c.hasLxc {
		vms, err := c.systemLXC(ctx)
		if err == nil {
			snap.VMs = append(snap.VMs, vms...)
		}
	}
	return nil
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

