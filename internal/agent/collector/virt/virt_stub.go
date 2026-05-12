//go:build !linux

// Package virt — non-Linux stub. The real implementation shells to
// virsh/lxc-ls/qm/pct and reads /etc/pve, all of which are Linux-specific.
// On other platforms we expose the same API surface so the agent package
// builds, but every probe reports unavailable.
package virt

import (
	"context"

	"github.com/MalteKiefer/MonSys/internal/shared/apitypes"
)

// Collector is the non-Linux placeholder. It has no fields because there is
// nothing to probe on platforms without libvirt/LXC/Proxmox tooling.
type Collector struct{}

// New constructs a stub Collector. It never reports Available()==true.
func New() *Collector { return &Collector{} }

// Name returns the collector identifier ("virt").
func (c *Collector) Name() string { return "virt" }

// Available always returns false on non-Linux platforms.
func (c *Collector) Available() bool { return false }

// Inventory is a no-op on non-Linux platforms.
func (c *Collector) Inventory(_ context.Context, _ *apitypes.InventorySnap) error { return nil }

// Collect is a no-op on non-Linux platforms.
func (c *Collector) Collect(_ context.Context, _ *apitypes.IngestRequest) error { return nil }
