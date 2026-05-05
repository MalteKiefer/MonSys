package apitypes

import "time"

// AgentRegisterRequest is sent by an agent on first start with the bootstrap token
// in the Authorization: Bearer header. The server responds with a per-host agent_key
// that must be used for subsequent /v1/ingest calls.
type AgentRegisterRequest struct {
	Hostname      string            `json:"hostname"        maxLength:"253" doc:"Operating-system hostname"     example:"db-01"`
	MachineID     string            `json:"machine_id"      maxLength:"64"  doc:"Contents of /etc/machine-id"  example:"a1b2c3..."`
	OS            string            `json:"os"              maxLength:"64"  doc:"GOOS, expected: linux"        example:"linux"`
	Kernel        string            `json:"kernel"          maxLength:"253" doc:"uname -r"                     example:"6.6.0-arch"`
	Arch          string            `json:"arch"            doc:"GOARCH"                       example:"amd64" enum:"amd64,arm64"`
	Distro        string            `json:"distro"          maxLength:"253" doc:"PRETTY_NAME from os-release"  example:"Ubuntu 24.04"`
	CPUModel      string            `json:"cpu_model"       maxLength:"253" doc:"Model name of CPU"`
	CPUCores      int               `json:"cpu_cores"       doc:"Logical core count"`
	RAMTotalBytes int64             `json:"ram_total_bytes" doc:"Total physical memory"`
	AgentVersion  string            `json:"agent_version"   maxLength:"64"  doc:"Version of mon-agent"`
	Labels        map[string]string `json:"labels,omitempty" doc:"Optional user-supplied labels"`
}

type AgentRegisterResponse struct {
	AgentID  string `json:"agent_id"  format:"uuid" maxLength:"36"  readOnly:"true" doc:"UUID assigned to this host"`
	AgentKey string `json:"agent_key" maxLength:"128"               readOnly:"true" doc:"Secret key for subsequent ingest calls; show only once"`
}

// IngestRequest contains a batch of metric samples and an optional inventory snapshot.
// Inventory is sent on first call after agent start and whenever the host inventory
// hash changes (new disk, removed NIC, …).
type IngestRequest struct {
	SnapshotAt time.Time            `json:"snapshot_at"          doc:"When the agent assembled this batch"`
	Inventory  *InventorySnap       `json:"inventory,omitempty"  doc:"Present only when changed"`
	System     []SystemSample       `json:"system,omitempty"`
	Disks      []DiskSample         `json:"disks,omitempty"`
	Nics       []NetSample          `json:"nics,omitempty"`
	Workloads  []WorkloadSample     `json:"workloads,omitempty"`
	Packages   *PackageReport       `json:"packages,omitempty"   doc:"Optional package state"`
	Security   *SecurityReport      `json:"security,omitempty"   doc:"Firewall, fail2ban, crowdsec snapshot"`
	Logins     []LoginEvent         `json:"logins,omitempty"     doc:"Incremental login/auth events since previous tick"`
}

type IngestResponse struct {
	Accepted   bool      `json:"accepted"`
	ServerTime time.Time `json:"server_time"`
}

type InventorySnap struct {
	Hostname      string            `json:"hostname"      maxLength:"253"`
	Kernel        string            `json:"kernel"        maxLength:"253"`
	Distro        string            `json:"distro"        maxLength:"253"`
	AgentVersion  string            `json:"agent_version" maxLength:"64"`
	CPUModel      string            `json:"cpu_model"     maxLength:"253"`
	CPUCores      int               `json:"cpu_cores"`
	RAMTotalBytes int64             `json:"ram_total_bytes"`
	Disks         []DiskInfo        `json:"disks,omitempty"`
	Nics          []NicInfo         `json:"nics,omitempty"`
	Workloads     []WorkloadInfo    `json:"workloads,omitempty"`
	VMs           []VMInfo          `json:"vms,omitempty"      doc:"libvirt/KVM domains and system LXC containers"`
	Users         []UserInfo        `json:"users,omitempty"    doc:"Local accounts from /etc/passwd"`
	Sources       []string          `json:"sources,omitempty"  doc:"Active collectors, e.g. docker, kubelet, proxmox"`
	Labels        map[string]string `json:"labels,omitempty"`
}

type VMInfo struct {
	Kind       string `json:"kind"        enum:"kvm,lxc,libvirt-lxc"`
	ExternalID string `json:"external_id" maxLength:"253" doc:"libvirt UUID or LXC name"`
	Name       string `json:"name"        maxLength:"253"`
	State      string `json:"state"       maxLength:"64"  doc:"running, paused, shut off, …"`
	VCPU       int    `json:"vcpu"`
	MemBytes   int64  `json:"mem_bytes"`
	Autostart  bool   `json:"autostart"`
}

type UserInfo struct {
	Username    string     `json:"username"           maxLength:"253"`
	UID         int        `json:"uid"`
	GID         int        `json:"gid"`
	Shell       string     `json:"shell,omitempty"    maxLength:"4096"`
	Home        string     `json:"home,omitempty"     maxLength:"4096"`
	IsSudoer    bool       `json:"is_sudoer"`
	IsSystem    bool       `json:"is_system"          doc:"uid < 1000"`
	LastLoginAt *time.Time `json:"last_login_at,omitempty"`
}

type LoginEvent struct {
	Time     time.Time `json:"time"`
	Username string    `json:"username,omitempty" maxLength:"253"`
	SourceIP string    `json:"source_ip,omitempty" maxLength:"64"`
	Method   string    `json:"method"             maxLength:"32"  doc:"ssh, login, su, sudo, …"`
	Success  bool      `json:"success"`
	Detail   string    `json:"detail,omitempty"   maxLength:"500"`
}

type SecurityReport struct {
	Time      time.Time            `json:"time"`
	Firewalls []FirewallStatus     `json:"firewalls,omitempty"`
	Fail2ban  []Fail2banJailInfo   `json:"fail2ban,omitempty"`
	CrowdSec  []CrowdsecDecision   `json:"crowdsec,omitempty"`
}

type FirewallStatus struct {
	Engine           string `json:"engine"            enum:"ufw,nftables,iptables"`
	Active           bool   `json:"active"`
	DefaultInput     string `json:"default_input,omitempty"   maxLength:"64"`
	DefaultOutput    string `json:"default_output,omitempty"  maxLength:"64"`
	DefaultForward   string `json:"default_forward,omitempty" maxLength:"64"`
	RuleCount        int    `json:"rule_count"`
	SnapshotExcerpt  string `json:"snapshot_excerpt,omitempty" maxLength:"10000" doc:"First ~4 KiB of the rule dump"`
}

type Fail2banJailInfo struct {
	Jail            string   `json:"jail"             maxLength:"100"`
	CurrentlyFailed int      `json:"currently_failed"`
	TotalFailed     int      `json:"total_failed"`
	CurrentlyBanned int      `json:"currently_banned"`
	TotalBanned     int      `json:"total_banned"`
	BannedIPs       []string `json:"banned_ips,omitempty"`
}

type CrowdsecDecision struct {
	DecisionID string    `json:"decision_id"        maxLength:"64"`
	Origin     string    `json:"origin,omitempty"   maxLength:"64"`
	Scope      string    `json:"scope,omitempty"    maxLength:"64"  doc:"Ip, Range, Country, AS, …"`
	Target     string    `json:"target,omitempty"   maxLength:"253"`
	Type       string    `json:"type,omitempty"     maxLength:"64"  doc:"ban, captcha, …"`
	Reason     string    `json:"reason,omitempty"   maxLength:"500"`
	Until      time.Time `json:"until,omitempty"`
}

type DiskInfo struct {
	Device      string `json:"device"     maxLength:"253"`
	Mountpoint  string `json:"mountpoint" maxLength:"4096"`
	FSType      string `json:"fstype"     maxLength:"64"`
	SizeBytes   int64  `json:"size_bytes"`
	IsRemovable bool   `json:"is_removable"`
}

type NicInfo struct {
	Name         string   `json:"name"      maxLength:"64"`
	MAC          string   `json:"mac"       maxLength:"64"`
	SpeedMbps    int      `json:"speed_mbps"`
	Addrs        []string `json:"addrs,omitempty"     doc:"IPv4 + IPv6 addresses with prefix length, e.g. 10.0.0.5/24, fe80::1/64"`
	Members      []string `json:"members,omitempty"      maxLength:"4096" doc:"Member interfaces if this is a bridge or bond"`
	BridgeMaster string   `json:"bridge_master,omitempty" maxLength:"64"   doc:"Master bridge/bond name when this NIC is enslaved"`
}

type WorkloadInfo struct {
	Kind       string            `json:"kind"        maxLength:"64"`
	ExternalID string            `json:"external_id" maxLength:"253"`
	Name       string            `json:"name"        maxLength:"253"`
	Image      string            `json:"image,omitempty" maxLength:"500"`
	State      string            `json:"state"       maxLength:"64"`
	Labels     map[string]string `json:"labels,omitempty"`
	// CurrentDigest is the runtime digest of the container's image as
	// reported by the local engine (e.g. via `docker inspect`). May be empty
	// for non-Docker workloads or when the engine has not exposed it yet.
	CurrentDigest string `json:"current_digest,omitempty" maxLength:"128"`
	// LatestDigest is the upstream registry's digest for the same image:tag.
	// Empty when the agent could not (or chose not to) reach the registry —
	// e.g. air-gapped host, anonymous-rate-limited, or digest-pinned image.
	LatestDigest string `json:"latest_digest,omitempty" maxLength:"128"`
	// UpdateAvailable is the agent's verdict — true iff CurrentDigest and
	// LatestDigest are both populated and differ. Servers persist this as
	// authoritative so the UI can render badges without re-comparing.
	UpdateAvailable bool `json:"update_available,omitempty"`
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
	Device       string    `json:"device"     maxLength:"253"`
	Mountpoint   string    `json:"mountpoint" maxLength:"4096"`
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
	NicName  string    `json:"nic_name" maxLength:"64"`
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
	Kind            string    `json:"kind"        maxLength:"64"`
	ExternalID      string    `json:"external_id" maxLength:"253"`
	CPUUsagePct     float64   `json:"cpu_usage_pct"`
	MemUsedBytes    int64     `json:"mem_used_bytes"`
	MemLimitBytes   int64     `json:"mem_limit_bytes"`
	NetRxBytes      int64     `json:"net_rx_bytes"`
	NetTxBytes      int64     `json:"net_tx_bytes"`
	BlockReadBytes  int64     `json:"block_read_bytes"`
	BlockWriteBytes int64     `json:"block_write_bytes"`
	State           string    `json:"state"             maxLength:"64"`
}

type PackageReport struct {
	Time           time.Time          `json:"time"`
	StateHash      string             `json:"state_hash"          maxLength:"128" doc:"sha256 over sorted (manager,name,version,arch); when unchanged, server may skip processing"`
	Installed      []InstalledPackage `json:"installed,omitempty" doc:"Full list; omit when state_hash unchanged"`
	Updates        []PendingUpdate    `json:"updates,omitempty"`
	RepoStates     []RepoMetaState    `json:"repo_states,omitempty"`
	Summary        PackageSummary     `json:"summary"`
}

type InstalledPackage struct {
	Manager     string    `json:"manager"     enum:"dpkg,rpm,pacman,apk"`
	Name        string    `json:"name"        maxLength:"253"`
	Version     string    `json:"version"     maxLength:"100"`
	Arch        string    `json:"arch,omitempty" maxLength:"32"`
	SourceRepo  string    `json:"source_repo,omitempty" maxLength:"253"`
	InstalledAt *time.Time `json:"installed_at,omitempty"`
}

type PendingUpdate struct {
	Manager          string `json:"manager"           enum:"dpkg,rpm,pacman,apk"`
	Name             string `json:"name"              maxLength:"253"`
	Arch             string `json:"arch,omitempty"    maxLength:"32"`
	CurrentVersion   string `json:"current_version"   maxLength:"100"`
	AvailableVersion string `json:"available_version" maxLength:"100"`
	SourceRepo       string `json:"source_repo,omitempty" maxLength:"253"`
	IsSecurity       bool   `json:"is_security"`
}

type RepoMetaState struct {
	Manager             string    `json:"manager"               maxLength:"32"`
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

// Notification rules

type NotificationRule struct {
	ID              string         `json:"id"               format:"uuid" maxLength:"36" readOnly:"true"`
	Name            string         `json:"name"             maxLength:"100"`
	Enabled         bool           `json:"enabled"`
	ConditionType   string         `json:"condition_type"   enum:"host_offline,monitor_failed,cert_expiring,login_failed_threshold,security_updates_pending"`
	ConditionParams map[string]any `json:"condition_params,omitempty"`
	ChannelIDs      []string       `json:"channel_ids"`
	Severity          string         `json:"severity"            enum:"info,warning,critical"`
	ThrottleSec       int            `json:"throttle_sec"        minimum:"0" doc:"0 disables throttling"`
	RepeatIntervalSec int            `json:"repeat_interval_sec" minimum:"0" doc:"0 fires once per outage; >=60 re-sends a reminder while still active"`
	NotifyOnResolve   bool           `json:"notify_on_resolve"   doc:"if false, suppresses the all-clear dispatch on recovery"`
	TargetHostIDs     []string       `json:"target_host_ids"     doc:"empty = all hosts"`
	TargetTags        []string       `json:"target_tags"         doc:"empty = ignore tag filter"`
	TargetGroupIDs    []string       `json:"target_group_ids"    doc:"empty = ignore group filter"`
	CreatedAt         time.Time      `json:"created_at"          readOnly:"true"`
	CreatedBy         string         `json:"created_by,omitempty" maxLength:"253" readOnly:"true"`
}

type NotificationRuleInput struct {
	Name              string         `json:"name"                minLength:"1" maxLength:"100"`
	Enabled           bool           `json:"enabled"`
	ConditionType     string         `json:"condition_type"      enum:"host_offline,monitor_failed,cert_expiring,login_failed_threshold,security_updates_pending"`
	ConditionParams   map[string]any `json:"condition_params,omitempty"`
	ChannelIDs        []string       `json:"channel_ids"         minItems:"1"`
	Severity          string         `json:"severity"            enum:"info,warning,critical"`
	ThrottleSec       int            `json:"throttle_sec"        minimum:"0"`
	RepeatIntervalSec int            `json:"repeat_interval_sec" minimum:"0"`
	NotifyOnResolve   bool           `json:"notify_on_resolve"`
	TargetHostIDs     []string       `json:"target_host_ids,omitempty"`
	TargetTags        []string       `json:"target_tags,omitempty"`
	TargetGroupIDs    []string       `json:"target_group_ids,omitempty"`
}

type AlertHistoryEntry struct {
	ID             int64          `json:"id"           readOnly:"true"`
	At             time.Time      `json:"at"           readOnly:"true"`
	RuleID         string         `json:"rule_id,omitempty"  format:"uuid" maxLength:"36"`
	RuleName       string         `json:"rule_name"    maxLength:"100"`
	Severity       string         `json:"severity"     enum:"info,warning,critical"`
	Subject        string         `json:"subject"      maxLength:"500"`
	Body           string         `json:"body"         maxLength:"10000"`
	DedupKey       string         `json:"dedup_key"    maxLength:"253"`
	DeliveredTo    []string       `json:"delivered_to"`
	DeliveryErrors map[string]any `json:"delivery_errors"`
}

// Active monitors (server-side periodic probes)

type Monitor struct {
	ID             string         `json:"id"             format:"uuid" maxLength:"36" readOnly:"true"`
	Type           string         `json:"type"           enum:"cert,postgres,mysql,mongodb,http,tcp"`
	Name           string         `json:"name"           maxLength:"100"`
	Target         string         `json:"target"         maxLength:"2048" doc:"host:port (cert/tcp), URL (http), DSN (db)"`
	Params         map[string]any `json:"params,omitempty"`
	IntervalSec    int            `json:"interval_sec"`
	Enabled        bool           `json:"enabled"`
	TargetTags     []string       `json:"target_tags"     doc:"Optional metadata: hosts with these tags this monitor relates to"`
	TargetGroupIDs []string       `json:"target_group_ids" doc:"Optional metadata: host groups this monitor relates to"`
	CreatedAt      time.Time      `json:"created_at"      readOnly:"true"`
	CreatedBy      string         `json:"created_by,omitempty"      maxLength:"253" readOnly:"true"`
	LastCheckAt    *time.Time     `json:"last_check_at,omitempty"   readOnly:"true"`
	LastStatus     string         `json:"last_status,omitempty"     enum:"ok,warn,fail,unknown" readOnly:"true"`
	LastLatencyMS  int            `json:"last_latency_ms,omitempty" readOnly:"true"`
	LastDetail     string         `json:"last_detail,omitempty"     maxLength:"500" readOnly:"true"`
}

type MonitorInput struct {
	Type           string         `json:"type"        enum:"cert,postgres,mysql,mongodb,http,tcp"`
	Name           string         `json:"name"        minLength:"1" maxLength:"100"`
	Target         string         `json:"target"      minLength:"1" maxLength:"2048"`
	Params         map[string]any `json:"params,omitempty"`
	IntervalSec    int            `json:"interval_sec" minimum:"10" maximum:"86400"`
	Enabled        bool           `json:"enabled"`
	TargetTags     []string       `json:"target_tags,omitempty"`
	TargetGroupIDs []string       `json:"target_group_ids,omitempty"`
}

type MonitorResult struct {
	Time      time.Time `json:"time"`
	Status    string    `json:"status"            enum:"ok,warn,fail,unknown"`
	LatencyMS int       `json:"latency_ms"`
	Detail    string    `json:"detail,omitempty"  maxLength:"500"`
}

// Notification channels

type NotificationChannel struct {
	ID             string         `json:"id"          format:"uuid" maxLength:"36" readOnly:"true"`
	Type           string         `json:"type"        enum:"email,slack,mattermost,discord,ntfy"`
	Name           string         `json:"name"        maxLength:"100"`
	Enabled        bool           `json:"enabled"`
	Config         map[string]any `json:"config"      doc:"Type-specific configuration (empty for email; recipient is in recipient_email)"`
	RecipientEmail string         `json:"recipient_email,omitempty" format:"email" maxLength:"254" doc:"Used by type=email; SMTP transport comes from the admin-managed global settings"`
	CreatedAt      time.Time      `json:"created_at"  readOnly:"true"`
	CreatedBy      string         `json:"created_by"  maxLength:"253" readOnly:"true"`
	OwnerUserID    string         `json:"owner_user_id,omitempty" format:"uuid" maxLength:"36" readOnly:"true"`
	LastUsedAt     *time.Time     `json:"last_used_at,omitempty"  readOnly:"true"`
	LastError      string         `json:"last_error,omitempty"    maxLength:"500" readOnly:"true"`
}

type NotificationChannelInput struct {
	Type           string         `json:"type"             enum:"email,slack,mattermost,discord,ntfy"`
	Name           string         `json:"name"             minLength:"1" maxLength:"100"`
	Enabled        bool           `json:"enabled"`
	Config         map[string]any `json:"config,omitempty" doc:"Type-specific config; ignored for type=email"`
	RecipientEmail string         `json:"recipient_email,omitempty" format:"email" maxLength:"254" doc:"Required for type=email; defaults to caller's account email if blank"`
}

// SmtpSettings is the admin-managed singleton describing the outbound mail
// transport. There is at most one row server-wide; type=email channels reuse
// it. Password is write-only: GET responses always blank it out.
type SmtpSettings struct {
	Host               string    `json:"host"                 maxLength:"253"`
	Port               int       `json:"port"`
	Username           string    `json:"username"             maxLength:"255"`
	HasPassword        bool      `json:"has_password"         doc:"True when a non-empty password is stored; the password itself is never returned"`
	FromAddress        string    `json:"from_address"         format:"email" maxLength:"254"`
	StartTLS           bool      `json:"starttls"`
	TLS                bool      `json:"tls"`
	InsecureSkipVerify bool      `json:"insecure_skip_verify"`
	UpdatedAt          time.Time `json:"updated_at"           readOnly:"true"`
	UpdatedBy          string    `json:"updated_by"           maxLength:"253" readOnly:"true"`
}

// SmtpSettingsInput is the admin-only PUT payload. Leaving Password empty
// preserves the stored value; submit "" with ClearPassword=true to wipe.
type SmtpSettingsInput struct {
	Host               string `json:"host"                  minLength:"1" maxLength:"255"`
	Port               int    `json:"port"                  minimum:"1"   maximum:"65535"`
	Username           string `json:"username,omitempty"    maxLength:"255"`
	Password           string `json:"password,omitempty"    maxLength:"128" writeOnly:"true" doc:"Leave empty to keep the stored password"`
	ClearPassword      bool   `json:"clear_password,omitempty" doc:"When true, the stored password is wiped"`
	FromAddress        string `json:"from_address"          format:"email" minLength:"3" maxLength:"255"`
	StartTLS           bool   `json:"starttls"`
	TLS                bool   `json:"tls"`
	InsecureSkipVerify bool   `json:"insecure_skip_verify"`
}

// SmtpTestRequest exercises the SMTP transport by sending a test message to
// To using the currently saved settings. Admin-only.
type SmtpTestRequest struct {
	To string `json:"to" format:"email" minLength:"3" maxLength:"255" doc:"Recipient address for the test mail"`
}

// NotificationSettings is the admin-managed singleton describing global
// outbound-alert behavior. Currently it only carries the quiet-hour window:
// when active, the alerts engine records the alert in alert_history (audit
// trail intact) but emits zero channel deliveries with a synthetic
// {"_quiet_hours":"suppressed"} marker.
type NotificationSettings struct {
	QuietEnabled bool      `json:"quiet_enabled"`
	QuietStart   string    `json:"quiet_start" maxLength:"5"  doc:"HH:MM, 24h, in QuietTZ" example:"22:00"`
	QuietEnd     string    `json:"quiet_end"   maxLength:"5"  doc:"HH:MM, 24h, in QuietTZ" example:"06:00"`
	QuietDays    []int     `json:"quiet_days"  doc:"0=Sun..6=Sat. Empty array means no day matches; default = every day"`
	QuietTZ      string    `json:"quiet_tz"    maxLength:"64" doc:"IANA name, e.g. UTC, Europe/Berlin" example:"UTC"`
	UpdatedAt    time.Time `json:"updated_at"  readOnly:"true"`
	UpdatedBy    string    `json:"updated_by"  maxLength:"253" readOnly:"true"`
}

// NotificationSettingsInput is the admin-only PUT payload.
type NotificationSettingsInput struct {
	QuietEnabled bool   `json:"quiet_enabled"`
	QuietStart   string `json:"quiet_start" pattern:"^([01]?[0-9]|2[0-3]):[0-5][0-9]$" example:"22:00"`
	QuietEnd     string `json:"quiet_end"   pattern:"^([01]?[0-9]|2[0-3]):[0-5][0-9]$" example:"06:00"`
	QuietDays    []int  `json:"quiet_days"  doc:"0=Sun..6=Sat. Each entry must be in 0..6"`
	QuietTZ      string `json:"quiet_tz"    minLength:"1" maxLength:"64" example:"UTC"`
}

type NotificationTestRequest struct {
	Subject string `json:"subject,omitempty" maxLength:"500"   doc:"Optional override; default 'mon test'"`
	Body    string `json:"body,omitempty"    maxLength:"10000" doc:"Optional override; default identifies channel"`
}

type NotificationTestResponse struct {
	OK    bool   `json:"ok"`
	Error string `json:"error,omitempty" maxLength:"500"`
}

// Auth (web users; distinct from agent_keys)

type LoginRequest struct {
	Email    string `json:"email"    format:"email" maxLength:"254" doc:"Login email"    example:"admin@example.com"`
	Password string `json:"password" maxLength:"128" writeOnly:"true" doc:"Plaintext password — TLS required"`
}

type LoginResponse struct {
	NeedsTOTP      bool      `json:"needs_totp"      doc:"true → call /v1/auth/2fa/challenge with challenge_token + code"`
	ChallengeToken string    `json:"challenge_token,omitempty" maxLength:"128" readOnly:"true"`
	Token          string    `json:"token,omitempty"           maxLength:"128" readOnly:"true" doc:"Session token; pass as Authorization: Bearer …"`
	ExpiresAt      time.Time `json:"expires_at,omitempty"      readOnly:"true" doc:"Session expiry (UTC)"`
	User           CurrentUser `json:"user"`
}

type CurrentUser struct {
	ID         string `json:"id"             format:"uuid"  maxLength:"36"  readOnly:"true"`
	Email      string `json:"email"          format:"email" maxLength:"254"`
	Role       string `json:"role"           enum:"admin,user" doc:"admin or user"`
	TOTPActive bool   `json:"totp_active"`
}

// AuthConfig surfaces server-wide auth/notification readiness flags to any
// logged-in user so the UI can warn before they create a channel that won't
// deliver. Hidden secrets stay on the admin endpoints.
type AuthConfig struct {
	SSOEnabled     bool `json:"sso_enabled"     doc:"True when an external SSO provider (e.g. Pocket-ID) is wired up"`
	SmtpConfigured bool `json:"smtp_configured" doc:"True when the global SMTP transport has Host + FromAddress set"`
}

// Self-service profile

type ChangePasswordRequest struct {
	CurrentPassword string `json:"current_password" maxLength:"128" writeOnly:"true"`
	NewPassword     string `json:"new_password"     maxLength:"128" writeOnly:"true"`
}

type ChangeEmailRequest struct {
	CurrentPassword string `json:"current_password" maxLength:"128" writeOnly:"true"`
	NewEmail        string `json:"new_email"        format:"email" maxLength:"254"`
}

type TOTPSetupResponse struct {
	SecretB32   string   `json:"secret_b32"    maxLength:"128"   readOnly:"true" doc:"raw TOTP secret in base32 — also encoded in otpauth_url + qr"`
	OTPAuthURL  string   `json:"otpauth_url"   format:"uri" maxLength:"2048" readOnly:"true" doc:"otpauth://totp/... uri compatible with most authenticators"`
	QRPNGBase64 string   `json:"qr_png_base64" maxLength:"65536" readOnly:"true" doc:"PNG QR code rendering of otpauth_url"`
	BackupCodes []string `json:"backup_codes"  readOnly:"true"   doc:"single-use recovery codes; show once"`
}

type TOTPVerifyRequest struct {
	Code string `json:"code" minLength:"6" maxLength:"10" doc:"current 6-digit TOTP, or a backup code"`
}

type TOTPDisableRequest struct {
	Password string `json:"password" maxLength:"128" writeOnly:"true" doc:"confirm by current password"`
}

// Login extension: 2FA challenge

type LoginChallenge struct {
	NeedsTOTP      bool      `json:"needs_totp"`
	ChallengeToken string    `json:"challenge_token,omitempty" maxLength:"128" readOnly:"true" doc:"intermediate token; pass to /v1/auth/2fa/challenge"`
	ExpiresAt      time.Time `json:"expires_at,omitempty"      readOnly:"true"`
}

type TOTPChallengeRequest struct {
	ChallengeToken string `json:"challenge_token" maxLength:"128"`
	Code           string `json:"code"            minLength:"6" maxLength:"10"`
}

// Admin

type AdminUserSummary struct {
	ID          string     `json:"id"           format:"uuid"  maxLength:"36"  readOnly:"true"`
	Email       string     `json:"email"        format:"email" maxLength:"254"`
	Role        string     `json:"role"         enum:"admin,user"`
	CreatedAt   time.Time  `json:"created_at"   readOnly:"true"`
	DisabledAt  *time.Time `json:"disabled_at,omitempty"   readOnly:"true"`
	TOTPActive  bool       `json:"totp_active"`
	LastLoginAt *time.Time `json:"last_login_at,omitempty" readOnly:"true"`
}

type AdminCreateUserRequest struct {
	Email      string `json:"email"      format:"email" maxLength:"254"`
	Role       string `json:"role"       enum:"admin,user"`
	Password   string `json:"password,omitempty" maxLength:"128" writeOnly:"true" doc:"if empty, an invite/reset token is issued and (if SMTP configured) emailed"`
	SendInvite bool   `json:"send_invite,omitempty"`
}

type AdminCreateUserResponse struct {
	User       AdminUserSummary `json:"user"`
	ResetURL   string           `json:"reset_url,omitempty"   format:"uri" maxLength:"2048" readOnly:"true" doc:"manual link if invite email was not delivered"`
	InviteSent bool             `json:"invite_sent"`
}

type AdminResetPasswordResponse struct {
	ResetURL    string `json:"reset_url,omitempty" format:"uri" maxLength:"2048" readOnly:"true"`
	InviteSent  bool   `json:"invite_sent"`
}

type PasswordPolicy struct {
	MinLength      int  `json:"min_length"      minimum:"4" maximum:"128"`
	RequireUpper   bool `json:"require_upper"`
	RequireLower   bool `json:"require_lower"`
	RequireDigit   bool `json:"require_digit"`
	RequireSymbol  bool `json:"require_symbol"`
	MaxAgeDays     int  `json:"max_age_days"    minimum:"0" doc:"0 disables age check"`
}

type ConsumeResetTokenRequest struct {
	Token       string `json:"token"        maxLength:"128" writeOnly:"true"`
	NewPassword string `json:"new_password" maxLength:"128" writeOnly:"true"`
}

// AuditEntry is a row from the audit_log table, surfaced via
// GET /v1/admin/audit. Detail is the marshaled JSON detail blob (free-form).
type AuditEntry struct {
	ID     int64     `json:"id"     readOnly:"true"`
	Actor  string    `json:"actor"  maxLength:"253"`
	Action string    `json:"action" maxLength:"100"`
	Target string    `json:"target" maxLength:"253"`
	Detail string    `json:"detail" maxLength:"10000"`
	At     time.Time `json:"at"     readOnly:"true"`
}

// Public read APIs (used by future UI)

// Host detail (single-host view)

type HostDetail struct {
	Host             Host                 `json:"host"`
	Disks            []DiskRow            `json:"disks"`
	Nics             []NicRow             `json:"nics"`
	Workloads        []WorkloadRow        `json:"workloads"`
	VMs              []VMRow              `json:"vms"`
	Users            []ObservedUser       `json:"users"`
	PackagesSummary  *PackageSummaryRow   `json:"packages_summary,omitempty"`
	RepoStates       []RepoMetaState      `json:"repo_states"`
}

type DiskRow struct {
	ID          string    `json:"id"          format:"uuid" maxLength:"36" readOnly:"true"`
	Device      string    `json:"device"      maxLength:"253"`
	Mountpoint  string    `json:"mountpoint"  maxLength:"4096"`
	FSType      string    `json:"fstype"      maxLength:"64"`
	SizeBytes   int64     `json:"size_bytes"`
	IsRemovable bool      `json:"is_removable"`
	LastSeenAt  time.Time `json:"last_seen_at" readOnly:"true"`
	// Latest sample (joined; zero values if no metric yet).
	LatestTime  *time.Time `json:"latest_time,omitempty" readOnly:"true"`
	UsedBytes   int64      `json:"used_bytes"`
	FreeBytes   int64      `json:"free_bytes"`
}

type NicRow struct {
	ID           string     `json:"id"           format:"uuid" maxLength:"36" readOnly:"true"`
	Name         string     `json:"name"         maxLength:"64"`
	MAC          string     `json:"mac"          maxLength:"64"`
	SpeedMbps    int        `json:"speed_mbps"`
	Addrs        []string   `json:"addrs"`
	Members      []string   `json:"members"      doc:"Member interfaces if this NIC is a bridge or bond; empty otherwise"`
	BridgeMaster string     `json:"bridge_master,omitempty" maxLength:"64" doc:"Master bridge/bond name when this NIC is enslaved"`
	LastSeenAt   time.Time  `json:"last_seen_at"  readOnly:"true"`
	LatestTime   *time.Time `json:"latest_time,omitempty" readOnly:"true"`
	RxBytes      int64      `json:"rx_bytes"`
	TxBytes      int64      `json:"tx_bytes"`
}

type WorkloadRow struct {
	ID         string            `json:"id"          format:"uuid" maxLength:"36" readOnly:"true"`
	Kind       string            `json:"kind"        maxLength:"64"`
	ExternalID string            `json:"external_id" maxLength:"253"`
	Name       string            `json:"name"        maxLength:"253"`
	Image      string            `json:"image,omitempty" maxLength:"500"`
	State      string            `json:"state"       maxLength:"64"`
	Labels     map[string]string `json:"labels,omitempty"`
	LastSeenAt time.Time         `json:"last_seen_at"  readOnly:"true"`
	LatestTime *time.Time        `json:"latest_time,omitempty" readOnly:"true"`
	CPUUsagePct float64          `json:"cpu_usage_pct"`
	MemUsedBytes int64            `json:"mem_used_bytes"`
	// CurrentDigest is the digest the container is currently running on; it
	// matches the local image at start time. Empty until the agent reports
	// it once.
	CurrentDigest string `json:"current_digest,omitempty" maxLength:"128"`
	// LatestDigest is the most recent upstream digest the agent observed for
	// the same image:tag. Empty when the lookup failed (offline host, rate
	// limit, digest-pinned reference, …).
	LatestDigest string `json:"latest_digest,omitempty" maxLength:"128"`
	// UpdateAvailable is the persisted verdict computed by the agent. The UI
	// uses this to render the "↑ update available" badge in Workloads.tsx.
	UpdateAvailable bool `json:"update_available"`
	// UpdateCheckedAt is the wall-clock time the server last accepted an
	// update-availability report from the agent. Useful for the UI to render
	// "checked Xm ago" tooltips.
	UpdateCheckedAt *time.Time `json:"update_checked_at,omitempty" readOnly:"true"`
}

type VMRow struct {
	Kind       string    `json:"kind"        maxLength:"64"`
	ExternalID string    `json:"external_id" maxLength:"253"`
	Name       string    `json:"name"        maxLength:"253"`
	State      string    `json:"state"       maxLength:"64"`
	VCPU       int       `json:"vcpu"`
	MemBytes   int64     `json:"mem_bytes"`
	Autostart  bool      `json:"autostart"`
	LastSeenAt time.Time `json:"last_seen_at" readOnly:"true"`
}

type ObservedUser struct {
	Username    string     `json:"username"           maxLength:"253"`
	UID         int        `json:"uid"`
	GID         int        `json:"gid"`
	Shell       string     `json:"shell,omitempty"    maxLength:"4096"`
	Home        string     `json:"home,omitempty"     maxLength:"4096"`
	IsSudoer    bool       `json:"is_sudoer"`
	IsSystem    bool       `json:"is_system"`
	LastLoginAt *time.Time `json:"last_login_at,omitempty" readOnly:"true"`
	LastSeenAt  time.Time  `json:"last_seen_at"            readOnly:"true"`
}

type PackageSummaryRow struct {
	Time            time.Time `json:"time"`
	InstalledCount  int       `json:"installed_count"`
	UpdatesCount    int       `json:"updates_count"`
	SecurityUpdates int       `json:"security_updates"`
	MetadataAgeSec  int64     `json:"metadata_age_seconds"`
}

// GlobalPackageRow is a search result row joining packages → hosts.
type GlobalPackageRow struct {
	HostID      string     `json:"host_id"     format:"uuid" maxLength:"36" readOnly:"true"`
	Hostname    string     `json:"hostname"    maxLength:"253"`
	Manager     string     `json:"manager"     maxLength:"32"`
	Name        string     `json:"name"        maxLength:"253"`
	Version     string     `json:"version"     maxLength:"100"`
	Arch        string     `json:"arch,omitempty" maxLength:"32"`
	SourceRepo  string     `json:"source_repo,omitempty" maxLength:"253"`
	InstalledAt *time.Time `json:"installed_at,omitempty"`
}

type PackageRow struct {
	Manager     string     `json:"manager"     maxLength:"32"`
	Name        string     `json:"name"        maxLength:"253"`
	Version     string     `json:"version"     maxLength:"100"`
	Arch        string     `json:"arch,omitempty" maxLength:"32"`
	SourceRepo  string     `json:"source_repo,omitempty" maxLength:"253"`
	InstalledAt *time.Time `json:"installed_at,omitempty"`
}

type Host struct {
	ID            string            `json:"id"            format:"uuid" maxLength:"36" readOnly:"true"`
	Hostname      string            `json:"hostname"      maxLength:"253"`
	Distro        string            `json:"distro"        maxLength:"253"`
	Arch          string            `json:"arch"          maxLength:"32"`
	CPUCores      int               `json:"cpu_cores"`
	RAMTotalBytes int64             `json:"ram_total_bytes"`
	AgentVersion  string            `json:"agent_version" maxLength:"64"`
	FirstSeenAt   time.Time         `json:"first_seen_at" readOnly:"true"`
	LastSeenAt    time.Time         `json:"last_seen_at"  readOnly:"true"`
	Status        string            `json:"status"        enum:"online,stale,offline,unknown"`
	StatusSince   time.Time         `json:"status_since,omitempty"  readOnly:"true"`
	Labels        map[string]string `json:"labels"`
	Tags          []string          `json:"tags"        doc:"Operator-set tags; managed via /v1/hosts/{id}/tags"`
	Groups        []HostGroupRef    `json:"groups"      doc:"Groups this host belongs to"`
	DistroFamily  string            `json:"distro_family,omitempty" maxLength:"32" doc:"arch/debian/ubuntu/fedora/rhel/alpine/suse — derived"`
	Services      []string          `json:"services,omitempty"      doc:"Detected services (postgres, redis, nginx, …)"`
	PendingUpdates  *int `json:"pending_updates,omitempty"  readOnly:"true" doc:"OS package updates pending; null when no package data"`
	SecurityUpdates *int `json:"security_updates,omitempty" readOnly:"true" doc:"OS security updates pending; null when no package data"`
}

type HostGroupRef struct {
	ID   string `json:"id"   format:"uuid" maxLength:"36"`
	Name string `json:"name" maxLength:"100"`
}

type HostGroup struct {
	ID          string    `json:"id"          format:"uuid" maxLength:"36" readOnly:"true"`
	Name        string    `json:"name"        maxLength:"100"`
	Description string    `json:"description,omitempty" maxLength:"500"`
	CreatedAt   time.Time `json:"created_at"  readOnly:"true"`
	CreatedBy   string    `json:"created_by,omitempty" maxLength:"253" readOnly:"true"`
	MemberIDs   []string  `json:"member_ids"`
}

type HostGroupInput struct {
	Name        string `json:"name"        minLength:"1" maxLength:"100"`
	Description string `json:"description,omitempty" maxLength:"500"`
}

type HostTagsInput struct {
	Tags []string `json:"tags" doc:"Replaces the host's tag set entirely"`
}

// AgentConfig is the JSON shape stored in agent_configs.config and shipped
// to agents via /v1/agent/config. Fields are optional so that a per-host
// override can change just one knob without re-stating the rest.
type AgentConfig struct {
	IntervalSeconds  *int                  `json:"interval_seconds,omitempty"  minimum:"5"  maximum:"3600"`
	BufferMaxMB      *int                  `json:"buffer_max_mb,omitempty"     minimum:"1"  maximum:"4096"`
	Packages         *AgentPackagesConfig  `json:"packages,omitempty"`
	QuietHours       *AgentQuietHours      `json:"quiet_hours,omitempty"`
	Schedules        []AgentSchedule       `json:"schedules,omitempty"`
	Labels           map[string]string     `json:"labels,omitempty"`
}

type AgentPackagesConfig struct {
	Enabled                 *bool   `json:"enabled,omitempty"`
	UpdateCheckInterval     *string `json:"update_check_interval,omitempty"      maxLength:"32" doc:"e.g. 30m, 2h"`
	FullSnapshotMaxInterval *string `json:"full_snapshot_max_interval,omitempty" maxLength:"32" doc:"e.g. 24h"`
}

// AgentQuietHours pauses ingest during a daily window in the agent's local
// timezone. Format HH:MM 24h. When start==end the agent treats it as
// disabled.
type AgentQuietHours struct {
	Enabled bool   `json:"enabled"`
	Start   string `json:"start" maxLength:"5" pattern:"^([01]?[0-9]|2[0-3]):[0-5][0-9]$"`
	End     string `json:"end"   maxLength:"5" pattern:"^([01]?[0-9]|2[0-3]):[0-5][0-9]$"`
	// Days of week when quiet hours apply, 0 = Sun..6 = Sat. Empty = every day.
	Days    []int  `json:"days,omitempty"`
}

// AgentSchedule lets operators raise or lower the tick rate during a window.
// e.g. "every 60s during business hours, every 300s overnight".
type AgentSchedule struct {
	Name            string `json:"name"             maxLength:"100"`
	Start           string `json:"start"            maxLength:"5"  pattern:"^([01]?[0-9]|2[0-3]):[0-5][0-9]$"`
	End             string `json:"end"              maxLength:"5"  pattern:"^([01]?[0-9]|2[0-3]):[0-5][0-9]$"`
	Days            []int  `json:"days,omitempty"`
	IntervalSeconds int    `json:"interval_seconds" minimum:"5" maximum:"3600"`
}

type AgentConfigEntry struct {
	ID          string      `json:"id"           format:"uuid" maxLength:"36" readOnly:"true"`
	Scope       string      `json:"scope"        enum:"global,group,host"`
	TargetID    string      `json:"target_id,omitempty"   maxLength:"64"  doc:"NULL for global"`
	TargetName  string      `json:"target_name,omitempty" maxLength:"253" doc:"hostname for host-scoped, group name for group-scoped"`
	Config      AgentConfig `json:"config"`
	Description string      `json:"description,omitempty" maxLength:"500"`
	Enabled     bool        `json:"enabled"`
	CreatedAt   time.Time   `json:"created_at"   readOnly:"true"`
	UpdatedAt   time.Time   `json:"updated_at"   readOnly:"true"`
	UpdatedBy   string      `json:"updated_by,omitempty"  maxLength:"253" readOnly:"true"`
}

type AgentConfigInput struct {
	Scope       string      `json:"scope"        enum:"global,group,host"`
	TargetID    string      `json:"target_id,omitempty"  maxLength:"64" doc:"required for scope=group or host"`
	Config      AgentConfig `json:"config"`
	Description string      `json:"description,omitempty" maxLength:"500"`
	Enabled     bool        `json:"enabled"`
}

// AgentConfigResolved is what the agent receives. It already merges
// host > group > global so the agent doesn't have to know about scopes.
type AgentConfigResolved struct {
	Config       AgentConfig `json:"config"`
	SourceScopes []string    `json:"source_scopes" doc:"Which scopes contributed; useful for the agent to log on apply."`
	FetchedAt    time.Time   `json:"fetched_at"`
}

type GroupMembersInput struct {
	HostIDs []string `json:"host_ids" doc:"Replaces the group's member set entirely"`
}

// AgentEnrollmentInput drives the "Add Agent" flow. The token created from this
// payload is single-use; default_tags / default_group_ids / default_label are
// applied when the agent first calls /v1/agents/register and never again.
type AgentEnrollmentInput struct {
	Label           string   `json:"label,omitempty"        maxLength:"100" doc:"Optional human label, displayed until the host is renamed"`
	Tags            []string `json:"tags,omitempty"         doc:"Tags applied on first registration"`
	GroupIDs        []string `json:"group_ids,omitempty"    doc:"Group memberships applied on first registration"`
	TTLMinutes      int      `json:"ttl_minutes,omitempty"  minimum:"5" maximum:"1440" doc:"Token lifetime; default 30, clamped to [5, 1440]"`
	Description     string   `json:"description,omitempty"  maxLength:"200" doc:"Free-form note shown in the enrollment list"`
}

// AgentEnrollment is the resource returned after creation. The plain-text token
// is exposed exactly once (in CreateResponse below) — listings and reads only
// surface metadata.
type AgentEnrollment struct {
	ID             string     `json:"id"                       format:"uuid" maxLength:"36"  readOnly:"true"`
	Label          string     `json:"label,omitempty"          maxLength:"100"`
	Description    string     `json:"description,omitempty"    maxLength:"200"`
	Tags           []string   `json:"tags"`
	GroupIDs       []string   `json:"group_ids"`
	ExpiresAt      time.Time  `json:"expires_at"               readOnly:"true"`
	CreatedAt      time.Time  `json:"created_at"               readOnly:"true"`
	CreatedBy      string     `json:"created_by,omitempty"     maxLength:"253" readOnly:"true"`
	UsedAt         *time.Time `json:"used_at,omitempty"        readOnly:"true"`
	UsedByHostID   string     `json:"used_by_host_id,omitempty" format:"uuid" maxLength:"36" readOnly:"true"`
	UsedByHostname string     `json:"used_by_hostname,omitempty" maxLength:"253" readOnly:"true"`
}

// AgentEnrollmentCreateResponse is returned only by POST. The token field is
// the only place the plain-text token is ever surfaced — store it client-side
// and show it once. Subsequent GETs omit it entirely.
type AgentEnrollmentCreateResponse struct {
	Enrollment     AgentEnrollment `json:"enrollment"`
	Token          string          `json:"token"           maxLength:"128" readOnly:"true" doc:"One-shot bootstrap token. Shown once; cannot be retrieved later."`
	InstallCommand string          `json:"install_command" maxLength:"4096" readOnly:"true" doc:"Single-line shell command the operator runs on the target host"`
	InstallURL     string          `json:"install_url"     format:"uri" maxLength:"2048" readOnly:"true" doc:"URL of the installer script (token included as ?t=…)"`
}
