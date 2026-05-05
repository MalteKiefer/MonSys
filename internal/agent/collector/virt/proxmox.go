// Proxmox VE discovery for the virt collector.
//
// Proxmox manages KVM VMs and LXC containers through its own stack (`qm`,
// `pct`, /etc/pve). libvirt and lxc-ls do not see those workloads, so we
// shell out to the Proxmox CLIs and merge the results into the existing
// VMInfo slice.
//
// Both `qm list` and `pct list` historically required root: /etc/pve is
// 0440 root:www-data and the CLIs read configuration from there. The agent
// runs as `monagent` and will silently get an error from those commands
// unless an operator grants the necessary access (e.g. ACL on /etc/pve or
// adding `monagent` to `www-data`). We do NOT attempt to fix that here —
// the collector degrades quietly when the calls fail, mirroring the
// libvirt/lxc-ls behaviour.
//
// TODO(operator): document a permission story (ExecStartPre setfacl on
// /etc/pve, or supplementary group www-data) in the install guide so the
// Proxmox path actually returns data on a vanilla install.

package virt

import (
	"bufio"
	"bytes"
	"context"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/MalteKiefer/MonSys/internal/agent/safeexec"
	"github.com/MalteKiefer/MonSys/internal/shared/apitypes"
)

// proxmoxAvailable reports whether this host looks like a Proxmox VE node:
// both `qm` and `pct` resolvable on SafePath, AND /etc/pve exists. The
// directory check is what distinguishes a real PVE install from a host that
// merely has the qemu-server / pve-container packages sitting around.
func proxmoxAvailable() bool {
	if !safeexec.Available("qm") || !safeexec.Available("pct") {
		return false
	}
	if _, err := os.Stat("/etc/pve"); err != nil {
		return false
	}
	return true
}

// proxmoxVMs enumerates Proxmox-managed QEMU VMs via `qm list`.
//
// Sample output:
//
//	      VMID NAME                 STATUS     MEM(MB)    BOOTDISK(GB) PID
//	       100 ubuntu-22.04         running    4096       20.00        12345
//	       200 windows-server       stopped    8192       50.00        0
//
// The header line is detected by a non-numeric first field; this also makes
// the parser tolerant of stray warning lines that PVE occasionally prints
// (e.g. "ipcc_send_rec[…] failed: Connection refused").
func (c *Collector) proxmoxVMs(ctx context.Context) ([]apitypes.VMInfo, error) {
	out, err := safeexec.RunWithTimeout(ctx, 8*time.Second, "qm", "list")
	if err != nil {
		return nil, err
	}
	var vms []apitypes.VMInfo
	sc := bufio.NewScanner(bytes.NewReader(out))
	for sc.Scan() {
		line := strings.TrimSpace(sc.Text())
		if line == "" {
			continue
		}
		f := strings.Fields(line)
		if len(f) < 4 {
			continue
		}
		vmid, err := strconv.Atoi(f[0])
		if err != nil {
			// Header ("VMID NAME ...") or warning line — skip.
			continue
		}
		v := apitypes.VMInfo{
			Kind:       "kvm",
			ExternalID: strconv.Itoa(vmid),
			Name:       f[1],
			State:      f[2],
		}
		if memMB, err := strconv.ParseInt(f[3], 10, 64); err == nil {
			v.MemBytes = memMB * 1024 * 1024
		}
		// VCPU is not in `qm list` output. Calling `qm config` per VM would
		// triple the syscall cost on a busy node; leave 0 and let a later
		// milestone enrich on demand.
		vms = append(vms, v)
	}
	return vms, sc.Err()
}

// proxmoxLXC enumerates Proxmox-managed LXC containers via `pct list`.
//
// Sample output:
//
//	VMID       Status     Lock         Name
//	113        running                 docker-host
//	200        stopped                 redis
//
// The Lock column is optional and may be empty, so we cannot rely on field
// count alone — we anchor on the numeric VMID and treat the trailing token
// as the name.
func (c *Collector) proxmoxLXC(ctx context.Context) ([]apitypes.VMInfo, error) {
	out, err := safeexec.RunWithTimeout(ctx, 8*time.Second, "pct", "list")
	if err != nil {
		return nil, err
	}
	var vms []apitypes.VMInfo
	sc := bufio.NewScanner(bytes.NewReader(out))
	for sc.Scan() {
		line := strings.TrimSpace(sc.Text())
		if line == "" {
			continue
		}
		f := strings.Fields(line)
		if len(f) < 2 {
			continue
		}
		ctid, err := strconv.Atoi(f[0])
		if err != nil {
			// Header line ("VMID Status Lock Name") or warning — skip.
			continue
		}
		v := apitypes.VMInfo{
			Kind:       "lxc",
			ExternalID: strconv.Itoa(ctid),
			State:      f[1],
		}
		// Name is the last whitespace-separated token. If it's missing,
		// fall back to the CTID so the entry still has a stable identifier.
		if len(f) >= 3 {
			v.Name = f[len(f)-1]
		} else {
			v.Name = strconv.Itoa(ctid)
		}
		vms = append(vms, v)
	}
	return vms, sc.Err()
}
