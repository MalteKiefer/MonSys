package apitypes

import "time"

// AgentRegisterRequest is sent by an agent on first start with the bootstrap token
// in the Authorization: Bearer header. The server responds with a per-host agent_key
// that must be used for subsequent /v1/ingest calls.
type AgentRegisterRequest struct {
	Hostname      string            `json:"hostname"        doc:"Operating-system hostname"     example:"db-01"`
	MachineID     string            `json:"machine_id"      doc:"Contents of /etc/machine-id"  example:"a1b2c3..."`
	OS            string            `json:"os"              doc:"GOOS, expected: linux"        example:"linux"`
	Kernel        string            `json:"kernel"          doc:"uname -r"                     example:"6.6.0-arch"`
	Arch          string            `json:"arch"            doc:"GOARCH"                       example:"amd64" enum:"amd64,arm64"`
	Distro        string            `json:"distro"          doc:"PRETTY_NAME from os-release"  example:"Ubuntu 24.04"`
	CPUModel      string            `json:"cpu_model"       doc:"Model name of CPU"`
	CPUCores      int               `json:"cpu_cores"       doc:"Logical core count"`
	RAMTotalBytes int64             `json:"ram_total_bytes" doc:"Total physical memory"`
	AgentVersion  string            `json:"agent_version"   doc:"Version of mon-agent"`
	Labels        map[string]string `json:"labels,omitempty" doc:"Optional user-supplied labels"`
}

type AgentRegisterResponse struct {
	AgentID  string `json:"agent_id"  doc:"UUID assigned to this host"`
	AgentKey string `json:"agent_key" doc:"Secret key for subsequent ingest calls; show only once"`
}

// IngestRequest contains a batch of metric samples and an optional inventory snapshot.
// Inventory is sent on first call after agent start and whenever the host inventory
// hash changes (new disk, removed NIC, …).
type IngestRequest struct {
	SnapshotAt time.Time         `json:"snapshot_at"          doc:"When the agent assembled this batch"`
	Inventory  *InventorySnap    `json:"inventory,omitempty"  doc:"Present only when changed"`
	System     []SystemSample    `json:"system,omitempty"`
	Disks      []DiskSample      `json:"disks,omitempty"`
	Nics       []NetSample       `json:"nics,omitempty"`
	Workloads  []WorkloadSample  `json:"workloads,omitempty"`
	Packages   *PackageReport    `json:"packages,omitempty"   doc:"Optional package state"`
}

type IngestResponse struct {
	Accepted   bool      `json:"accepted"`
	ServerTime time.Time `json:"server_time"`
}

type InventorySnap struct {
	Hostname      string            `json:"hostname"`
	Kernel        string            `json:"kernel"`
	Distro        string            `json:"distro"`
	AgentVersion  string            `json:"agent_version"`
	CPUModel      string            `json:"cpu_model"`
	CPUCores      int               `json:"cpu_cores"`
	RAMTotalBytes int64             `json:"ram_total_bytes"`
	Disks         []DiskInfo        `json:"disks,omitempty"`
	Nics          []NicInfo         `json:"nics,omitempty"`
	Workloads     []WorkloadInfo    `json:"workloads,omitempty"`
	Sources       []string          `json:"sources,omitempty"  doc:"Active collectors, e.g. docker, kubelet, proxmox"`
	Labels        map[string]string `json:"labels,omitempty"`
}

type DiskInfo struct {
	Device      string `json:"device"`
	Mountpoint  string `json:"mountpoint"`
	FSType      string `json:"fstype"`
	SizeBytes   int64  `json:"size_bytes"`
	IsRemovable bool   `json:"is_removable"`
}

type NicInfo struct {
	Name      string `json:"name"`
	MAC       string `json:"mac"`
	SpeedMbps int    `json:"speed_mbps"`
}

type WorkloadInfo struct {
	Kind       string            `json:"kind"`
	ExternalID string            `json:"external_id"`
	Name       string            `json:"name"`
	Image      string            `json:"image,omitempty"`
	State      string            `json:"state"`
	Labels     map[string]string `json:"labels,omitempty"`
}

type SystemSample struct {
	Time          time.Time `json:"time"`
	CPUUsagePct   float64   `json:"cpu_usage_pct"`
	CPUPerCore    []float64 `json:"cpu_per_core,omitempty"`
	Load1         float64   `json:"load_1"`
	Load5         float64   `json:"load_5"`
	Load15        float64   `json:"load_15"`
	RAMUsedBytes  int64     `json:"ram_used_bytes"`
	RAMAvailBytes int64     `json:"ram_avail_bytes"`
	SwapUsedBytes int64     `json:"swap_used_bytes"`
	UptimeSec     int64     `json:"uptime_sec"`
}

type DiskSample struct {
	Time         time.Time `json:"time"`
	Device       string    `json:"device"`
	Mountpoint   string    `json:"mountpoint"`
	UsedBytes    int64     `json:"used_bytes"`
	FreeBytes    int64     `json:"free_bytes"`
	InodesUsed   int64     `json:"inodes_used"`
	InodesFree   int64     `json:"inodes_free"`
	ReadBytes    int64     `json:"read_bytes"`
	WriteBytes   int64     `json:"write_bytes"`
	ReadOps      int64     `json:"read_ops"`
	WriteOps     int64     `json:"write_ops"`
	IOTimeMS     int64     `json:"io_time_ms"`
}

type NetSample struct {
	Time     time.Time `json:"time"`
	NicName  string    `json:"nic_name"`
	RxBytes  int64     `json:"rx_bytes"`
	TxBytes  int64     `json:"tx_bytes"`
	RxPkts   int64     `json:"rx_pkts"`
	TxPkts   int64     `json:"tx_pkts"`
	RxErrs   int64     `json:"rx_errs"`
	TxErrs   int64     `json:"tx_errs"`
	RxDrops  int64     `json:"rx_drops"`
	TxDrops  int64     `json:"tx_drops"`
}

type WorkloadSample struct {
	Time            time.Time `json:"time"`
	Kind            string    `json:"kind"`
	ExternalID      string    `json:"external_id"`
	CPUUsagePct     float64   `json:"cpu_usage_pct"`
	MemUsedBytes    int64     `json:"mem_used_bytes"`
	MemLimitBytes   int64     `json:"mem_limit_bytes"`
	NetRxBytes      int64     `json:"net_rx_bytes"`
	NetTxBytes      int64     `json:"net_tx_bytes"`
	BlockReadBytes  int64     `json:"block_read_bytes"`
	BlockWriteBytes int64     `json:"block_write_bytes"`
	State           string    `json:"state"`
}

type PackageReport struct {
	Time           time.Time          `json:"time"`
	StateHash      string             `json:"state_hash"          doc:"sha256 over sorted (manager,name,version,arch); when unchanged, server may skip processing"`
	Installed      []InstalledPackage `json:"installed,omitempty" doc:"Full list; omit when state_hash unchanged"`
	Updates        []PendingUpdate    `json:"updates,omitempty"`
	RepoStates     []RepoMetaState    `json:"repo_states,omitempty"`
	Summary        PackageSummary     `json:"summary"`
}

type InstalledPackage struct {
	Manager     string    `json:"manager"     enum:"dpkg,rpm,pacman,apk"`
	Name        string    `json:"name"`
	Version     string    `json:"version"`
	Arch        string    `json:"arch,omitempty"`
	SourceRepo  string    `json:"source_repo,omitempty"`
	InstalledAt *time.Time `json:"installed_at,omitempty"`
}

type PendingUpdate struct {
	Manager          string `json:"manager"           enum:"dpkg,rpm,pacman,apk"`
	Name             string `json:"name"`
	Arch             string `json:"arch,omitempty"`
	CurrentVersion   string `json:"current_version"`
	AvailableVersion string `json:"available_version"`
	SourceRepo       string `json:"source_repo,omitempty"`
	IsSecurity       bool   `json:"is_security"`
}

type RepoMetaState struct {
	Manager             string    `json:"manager"`
	MetadataMtime       time.Time `json:"metadata_mtime"`
	MetadataAgeSec      int64     `json:"metadata_age_seconds"`
	RefreshedExternally bool      `json:"refreshed_externally"`
}

type PackageSummary struct {
	InstalledCount  int   `json:"installed_count"`
	UpdatesCount    int   `json:"updates_count"`
	SecurityUpdates int   `json:"security_updates"`
	MetadataAgeSec  int64 `json:"metadata_age_seconds"`
}

// Public read APIs (used by future UI)

type Host struct {
	ID            string            `json:"id"`
	Hostname      string            `json:"hostname"`
	Distro        string            `json:"distro"`
	Arch          string            `json:"arch"`
	CPUCores      int               `json:"cpu_cores"`
	RAMTotalBytes int64             `json:"ram_total_bytes"`
	AgentVersion  string            `json:"agent_version"`
	FirstSeenAt   time.Time         `json:"first_seen_at"`
	LastSeenAt    time.Time         `json:"last_seen_at"`
	Labels        map[string]string `json:"labels"`
}
