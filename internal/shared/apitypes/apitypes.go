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
	VMs           []VMInfo          `json:"vms,omitempty"      doc:"libvirt/KVM domains and system LXC containers"`
	Users         []UserInfo        `json:"users,omitempty"    doc:"Local accounts from /etc/passwd"`
	Sources       []string          `json:"sources,omitempty"  doc:"Active collectors, e.g. docker, kubelet, proxmox"`
	Labels        map[string]string `json:"labels,omitempty"`
}

type VMInfo struct {
	Kind       string `json:"kind"        enum:"kvm,lxc,libvirt-lxc"`
	ExternalID string `json:"external_id" doc:"libvirt UUID or LXC name"`
	Name       string `json:"name"`
	State      string `json:"state"       doc:"running, paused, shut off, …"`
	VCPU       int    `json:"vcpu"`
	MemBytes   int64  `json:"mem_bytes"`
	Autostart  bool   `json:"autostart"`
}

type UserInfo struct {
	Username    string     `json:"username"`
	UID         int        `json:"uid"`
	GID         int        `json:"gid"`
	Shell       string     `json:"shell,omitempty"`
	Home        string     `json:"home,omitempty"`
	IsSudoer    bool       `json:"is_sudoer"`
	IsSystem    bool       `json:"is_system"   doc:"uid < 1000"`
	LastLoginAt *time.Time `json:"last_login_at,omitempty"`
}

type LoginEvent struct {
	Time     time.Time `json:"time"`
	Username string    `json:"username,omitempty"`
	SourceIP string    `json:"source_ip,omitempty"`
	Method   string    `json:"method"   doc:"ssh, login, su, sudo, …"`
	Success  bool      `json:"success"`
	Detail   string    `json:"detail,omitempty"`
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
	DefaultInput     string `json:"default_input,omitempty"`
	DefaultOutput    string `json:"default_output,omitempty"`
	DefaultForward   string `json:"default_forward,omitempty"`
	RuleCount        int    `json:"rule_count"`
	SnapshotExcerpt  string `json:"snapshot_excerpt,omitempty" doc:"First ~4 KiB of the rule dump"`
}

type Fail2banJailInfo struct {
	Jail            string   `json:"jail"`
	CurrentlyFailed int      `json:"currently_failed"`
	TotalFailed     int      `json:"total_failed"`
	CurrentlyBanned int      `json:"currently_banned"`
	TotalBanned     int      `json:"total_banned"`
	BannedIPs       []string `json:"banned_ips,omitempty"`
}

type CrowdsecDecision struct {
	DecisionID string    `json:"decision_id"`
	Origin     string    `json:"origin,omitempty"`
	Scope      string    `json:"scope,omitempty"   doc:"Ip, Range, Country, AS, …"`
	Target     string    `json:"target,omitempty"`
	Type       string    `json:"type,omitempty"    doc:"ban, captcha, …"`
	Reason     string    `json:"reason,omitempty"`
	Until      time.Time `json:"until,omitempty"`
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

// Notification rules

type NotificationRule struct {
	ID              string         `json:"id"`
	Name            string         `json:"name"`
	Enabled         bool           `json:"enabled"`
	ConditionType   string         `json:"condition_type"   enum:"host_offline,monitor_failed,cert_expiring,login_failed_threshold,security_updates_pending"`
	ConditionParams map[string]any `json:"condition_params,omitempty"`
	ChannelIDs      []string       `json:"channel_ids"`
	Severity        string         `json:"severity"         enum:"info,warning,critical"`
	ThrottleSec     int            `json:"throttle_sec"     minimum:"0" doc:"0 disables throttling"`
	CreatedAt       time.Time      `json:"created_at"`
	CreatedBy       string         `json:"created_by,omitempty"`
}

type NotificationRuleInput struct {
	Name            string         `json:"name"            minLength:"1" maxLength:"100"`
	Enabled         bool           `json:"enabled"`
	ConditionType   string         `json:"condition_type"  enum:"host_offline,monitor_failed,cert_expiring,login_failed_threshold,security_updates_pending"`
	ConditionParams map[string]any `json:"condition_params,omitempty"`
	ChannelIDs      []string       `json:"channel_ids"     minItems:"1"`
	Severity        string         `json:"severity"        enum:"info,warning,critical"`
	ThrottleSec     int            `json:"throttle_sec"    minimum:"0"`
}

type AlertHistoryEntry struct {
	ID             int64          `json:"id"`
	At             time.Time      `json:"at"`
	RuleID         string         `json:"rule_id,omitempty"`
	RuleName       string         `json:"rule_name"`
	Severity       string         `json:"severity"`
	Subject        string         `json:"subject"`
	Body           string         `json:"body"`
	DedupKey       string         `json:"dedup_key"`
	DeliveredTo    []string       `json:"delivered_to"`
	DeliveryErrors map[string]any `json:"delivery_errors"`
}

// Active monitors (server-side periodic probes)

type Monitor struct {
	ID             string         `json:"id"`
	Type           string         `json:"type"           enum:"cert,postgres,mysql,mongodb,http,tcp"`
	Name           string         `json:"name"`
	Target         string         `json:"target"         doc:"host:port (cert/tcp), URL (http), DSN (db)"`
	Params         map[string]any `json:"params,omitempty"`
	IntervalSec    int            `json:"interval_sec"`
	Enabled        bool           `json:"enabled"`
	TargetTags     []string       `json:"target_tags"     doc:"Optional metadata: hosts with these tags this monitor relates to"`
	TargetGroupIDs []string       `json:"target_group_ids" doc:"Optional metadata: host groups this monitor relates to"`
	CreatedAt      time.Time      `json:"created_at"`
	CreatedBy      string         `json:"created_by,omitempty"`
	LastCheckAt    *time.Time     `json:"last_check_at,omitempty"`
	LastStatus     string         `json:"last_status,omitempty"  enum:"ok,warn,fail,unknown"`
	LastLatencyMS  int            `json:"last_latency_ms,omitempty"`
	LastDetail     string         `json:"last_detail,omitempty"`
}

type MonitorInput struct {
	Type           string         `json:"type"        enum:"cert,postgres,mysql,mongodb,http,tcp"`
	Name           string         `json:"name"        minLength:"1" maxLength:"100"`
	Target         string         `json:"target"      minLength:"1"`
	Params         map[string]any `json:"params,omitempty"`
	IntervalSec    int            `json:"interval_sec" minimum:"10" maximum:"86400"`
	Enabled        bool           `json:"enabled"`
	TargetTags     []string       `json:"target_tags,omitempty"`
	TargetGroupIDs []string       `json:"target_group_ids,omitempty"`
}

type MonitorResult struct {
	Time      time.Time `json:"time"`
	Status    string    `json:"status"`
	LatencyMS int       `json:"latency_ms"`
	Detail    string    `json:"detail,omitempty"`
}

// Notification channels

type NotificationChannel struct {
	ID             string         `json:"id"`
	Type           string         `json:"type"        enum:"email,slack,mattermost,discord,ntfy"`
	Name           string         `json:"name"`
	Enabled        bool           `json:"enabled"`
	Config         map[string]any `json:"config"      doc:"Type-specific configuration (empty for email; recipient is in recipient_email)"`
	RecipientEmail string         `json:"recipient_email,omitempty" doc:"Used by type=email; SMTP transport comes from the admin-managed global settings"`
	CreatedAt      time.Time      `json:"created_at"`
	CreatedBy      string         `json:"created_by"`
	OwnerUserID    string         `json:"owner_user_id,omitempty"`
	LastUsedAt     *time.Time     `json:"last_used_at,omitempty"`
	LastError      string         `json:"last_error,omitempty"`
}

type NotificationChannelInput struct {
	Type           string         `json:"type"             enum:"email,slack,mattermost,discord,ntfy"`
	Name           string         `json:"name"             minLength:"1" maxLength:"100"`
	Enabled        bool           `json:"enabled"`
	Config         map[string]any `json:"config,omitempty" doc:"Type-specific config; ignored for type=email"`
	RecipientEmail string         `json:"recipient_email,omitempty" doc:"Required for type=email; defaults to caller's account email if blank"`
}

// SmtpSettings is the admin-managed singleton describing the outbound mail
// transport. There is at most one row server-wide; type=email channels reuse
// it. Password is write-only: GET responses always blank it out.
type SmtpSettings struct {
	Host               string    `json:"host"`
	Port               int       `json:"port"`
	Username           string    `json:"username"`
	HasPassword        bool      `json:"has_password" doc:"True when a non-empty password is stored; the password itself is never returned"`
	FromAddress        string    `json:"from_address"`
	StartTLS           bool      `json:"starttls"`
	TLS                bool      `json:"tls"`
	InsecureSkipVerify bool      `json:"insecure_skip_verify"`
	UpdatedAt          time.Time `json:"updated_at"`
	UpdatedBy          string    `json:"updated_by"`
}

// SmtpSettingsInput is the admin-only PUT payload. Leaving Password empty
// preserves the stored value; submit "" with ClearPassword=true to wipe.
type SmtpSettingsInput struct {
	Host               string `json:"host"                  minLength:"1" maxLength:"255"`
	Port               int    `json:"port"                  minimum:"1"   maximum:"65535"`
	Username           string `json:"username,omitempty"    maxLength:"255"`
	Password           string `json:"password,omitempty"    doc:"Leave empty to keep the stored password"`
	ClearPassword      bool   `json:"clear_password,omitempty" doc:"When true, the stored password is wiped"`
	FromAddress        string `json:"from_address"          minLength:"3" maxLength:"255"`
	StartTLS           bool   `json:"starttls"`
	TLS                bool   `json:"tls"`
	InsecureSkipVerify bool   `json:"insecure_skip_verify"`
}

// SmtpTestRequest exercises the SMTP transport by sending a test message to
// To using the currently saved settings. Admin-only.
type SmtpTestRequest struct {
	To string `json:"to" minLength:"3" maxLength:"255" doc:"Recipient address for the test mail"`
}

// NotificationSettings is the admin-managed singleton describing global
// outbound-alert behavior. Currently it only carries the quiet-hour window:
// when active, the alerts engine records the alert in alert_history (audit
// trail intact) but emits zero channel deliveries with a synthetic
// {"_quiet_hours":"suppressed"} marker.
type NotificationSettings struct {
	QuietEnabled bool      `json:"quiet_enabled"`
	QuietStart   string    `json:"quiet_start" doc:"HH:MM, 24h, in QuietTZ" example:"22:00"`
	QuietEnd     string    `json:"quiet_end"   doc:"HH:MM, 24h, in QuietTZ" example:"06:00"`
	QuietDays    []int     `json:"quiet_days"  doc:"0=Sun..6=Sat. Empty array means no day matches; default = every day"`
	QuietTZ      string    `json:"quiet_tz"    doc:"IANA name, e.g. UTC, Europe/Berlin" example:"UTC"`
	UpdatedAt    time.Time `json:"updated_at"`
	UpdatedBy    string    `json:"updated_by"`
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
	Subject string `json:"subject,omitempty" doc:"Optional override; default 'mon test'"`
	Body    string `json:"body,omitempty"    doc:"Optional override; default identifies channel"`
}

type NotificationTestResponse struct {
	OK    bool   `json:"ok"`
	Error string `json:"error,omitempty"`
}

// Auth (web users; distinct from agent_keys)

type LoginRequest struct {
	Email    string `json:"email"    doc:"Login email"    example:"admin@example.com"`
	Password string `json:"password" doc:"Plaintext password — TLS required"`
}

type LoginResponse struct {
	NeedsTOTP      bool      `json:"needs_totp"      doc:"true → call /v1/auth/2fa/challenge with challenge_token + code"`
	ChallengeToken string    `json:"challenge_token,omitempty"`
	Token          string    `json:"token,omitempty"           doc:"Session token; pass as Authorization: Bearer …"`
	ExpiresAt      time.Time `json:"expires_at,omitempty"      doc:"Session expiry (UTC)"`
	User           CurrentUser `json:"user"`
}

type CurrentUser struct {
	ID         string `json:"id"`
	Email      string `json:"email"`
	Role       string `json:"role"           doc:"admin or user"`
	TOTPActive bool   `json:"totp_active"`
}

// Self-service profile

type ChangePasswordRequest struct {
	CurrentPassword string `json:"current_password"`
	NewPassword     string `json:"new_password"`
}

type ChangeEmailRequest struct {
	CurrentPassword string `json:"current_password"`
	NewEmail        string `json:"new_email"        format:"email"`
}

type TOTPSetupResponse struct {
	SecretB32   string   `json:"secret_b32"   doc:"raw TOTP secret in base32 — also encoded in otpauth_url + qr"`
	OTPAuthURL  string   `json:"otpauth_url"  doc:"otpauth://totp/... uri compatible with most authenticators"`
	QRPNGBase64 string   `json:"qr_png_base64" doc:"PNG QR code rendering of otpauth_url"`
	BackupCodes []string `json:"backup_codes" doc:"single-use recovery codes; show once"`
}

type TOTPVerifyRequest struct {
	Code string `json:"code" minLength:"6" maxLength:"10" doc:"current 6-digit TOTP, or a backup code"`
}

type TOTPDisableRequest struct {
	Password string `json:"password" doc:"confirm by current password"`
}

// Login extension: 2FA challenge

type LoginChallenge struct {
	NeedsTOTP      bool      `json:"needs_totp"`
	ChallengeToken string    `json:"challenge_token,omitempty" doc:"intermediate token; pass to /v1/auth/2fa/challenge"`
	ExpiresAt      time.Time `json:"expires_at,omitempty"`
}

type TOTPChallengeRequest struct {
	ChallengeToken string `json:"challenge_token"`
	Code           string `json:"code"`
}

// Admin

type AdminUserSummary struct {
	ID          string     `json:"id"`
	Email       string     `json:"email"`
	Role        string     `json:"role"`
	CreatedAt   time.Time  `json:"created_at"`
	DisabledAt  *time.Time `json:"disabled_at,omitempty"`
	TOTPActive  bool       `json:"totp_active"`
	LastLoginAt *time.Time `json:"last_login_at,omitempty"`
}

type AdminCreateUserRequest struct {
	Email      string `json:"email"      format:"email"`
	Role       string `json:"role"       enum:"admin,user"`
	Password   string `json:"password,omitempty" doc:"if empty, an invite/reset token is issued and (if SMTP configured) emailed"`
	SendInvite bool   `json:"send_invite,omitempty"`
}

type AdminCreateUserResponse struct {
	User       AdminUserSummary `json:"user"`
	ResetURL   string           `json:"reset_url,omitempty"   doc:"manual link if invite email was not delivered"`
	InviteSent bool             `json:"invite_sent"`
}

type AdminResetPasswordResponse struct {
	ResetURL    string `json:"reset_url,omitempty"`
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
	Token       string `json:"token"`
	NewPassword string `json:"new_password"`
}

// AuditEntry is a row from the audit_log table, surfaced via
// GET /v1/admin/audit. Detail is the marshaled JSON detail blob (free-form).
type AuditEntry struct {
	ID     int64     `json:"id"`
	Actor  string    `json:"actor"`
	Action string    `json:"action"`
	Target string    `json:"target"`
	Detail string    `json:"detail"`
	At     time.Time `json:"at"`
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
	ID          string    `json:"id"`
	Device      string    `json:"device"`
	Mountpoint  string    `json:"mountpoint"`
	FSType      string    `json:"fstype"`
	SizeBytes   int64     `json:"size_bytes"`
	IsRemovable bool      `json:"is_removable"`
	LastSeenAt  time.Time `json:"last_seen_at"`
	// Latest sample (joined; zero values if no metric yet).
	LatestTime  *time.Time `json:"latest_time,omitempty"`
	UsedBytes   int64      `json:"used_bytes"`
	FreeBytes   int64      `json:"free_bytes"`
}

type NicRow struct {
	ID         string     `json:"id"`
	Name       string     `json:"name"`
	MAC        string     `json:"mac"`
	SpeedMbps  int        `json:"speed_mbps"`
	LastSeenAt time.Time  `json:"last_seen_at"`
	LatestTime *time.Time `json:"latest_time,omitempty"`
	RxBytes    int64      `json:"rx_bytes"`
	TxBytes    int64      `json:"tx_bytes"`
}

type WorkloadRow struct {
	ID         string            `json:"id"`
	Kind       string            `json:"kind"`
	ExternalID string            `json:"external_id"`
	Name       string            `json:"name"`
	Image      string            `json:"image,omitempty"`
	State      string            `json:"state"`
	Labels     map[string]string `json:"labels,omitempty"`
	LastSeenAt time.Time         `json:"last_seen_at"`
	LatestTime *time.Time        `json:"latest_time,omitempty"`
	CPUUsagePct float64          `json:"cpu_usage_pct"`
	MemUsedBytes int64            `json:"mem_used_bytes"`
}

type VMRow struct {
	Kind       string    `json:"kind"`
	ExternalID string    `json:"external_id"`
	Name       string    `json:"name"`
	State      string    `json:"state"`
	VCPU       int       `json:"vcpu"`
	MemBytes   int64     `json:"mem_bytes"`
	Autostart  bool      `json:"autostart"`
	LastSeenAt time.Time `json:"last_seen_at"`
}

type ObservedUser struct {
	Username    string     `json:"username"`
	UID         int        `json:"uid"`
	GID         int        `json:"gid"`
	Shell       string     `json:"shell,omitempty"`
	Home        string     `json:"home,omitempty"`
	IsSudoer    bool       `json:"is_sudoer"`
	IsSystem    bool       `json:"is_system"`
	LastLoginAt *time.Time `json:"last_login_at,omitempty"`
	LastSeenAt  time.Time  `json:"last_seen_at"`
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
	HostID      string     `json:"host_id"`
	Hostname    string     `json:"hostname"`
	Manager     string     `json:"manager"`
	Name        string     `json:"name"`
	Version     string     `json:"version"`
	Arch        string     `json:"arch,omitempty"`
	SourceRepo  string     `json:"source_repo,omitempty"`
	InstalledAt *time.Time `json:"installed_at,omitempty"`
}

type PackageRow struct {
	Manager     string     `json:"manager"`
	Name        string     `json:"name"`
	Version     string     `json:"version"`
	Arch        string     `json:"arch,omitempty"`
	SourceRepo  string     `json:"source_repo,omitempty"`
	InstalledAt *time.Time `json:"installed_at,omitempty"`
}

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
	Status        string            `json:"status"      enum:"online,stale,offline,unknown"`
	StatusSince   time.Time         `json:"status_since,omitempty"`
	Labels        map[string]string `json:"labels"`
	Tags          []string          `json:"tags"        doc:"Operator-set tags; managed via /v1/hosts/{id}/tags"`
	Groups        []HostGroupRef    `json:"groups"      doc:"Groups this host belongs to"`
	DistroFamily  string            `json:"distro_family,omitempty" doc:"arch/debian/ubuntu/fedora/rhel/alpine/suse — derived"`
	Services      []string          `json:"services,omitempty"      doc:"Detected services (postgres, redis, nginx, …)"`
}

type HostGroupRef struct {
	ID   string `json:"id"`
	Name string `json:"name"`
}

type HostGroup struct {
	ID          string    `json:"id"`
	Name        string    `json:"name"`
	Description string    `json:"description,omitempty"`
	CreatedAt   time.Time `json:"created_at"`
	CreatedBy   string    `json:"created_by,omitempty"`
	MemberIDs   []string  `json:"member_ids"`
}

type HostGroupInput struct {
	Name        string `json:"name"        minLength:"1" maxLength:"100"`
	Description string `json:"description,omitempty"`
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
	UpdateCheckInterval     *string `json:"update_check_interval,omitempty"      doc:"e.g. 30m, 2h"`
	FullSnapshotMaxInterval *string `json:"full_snapshot_max_interval,omitempty" doc:"e.g. 24h"`
}

// AgentQuietHours pauses ingest during a daily window in the agent's local
// timezone. Format HH:MM 24h. When start==end the agent treats it as
// disabled.
type AgentQuietHours struct {
	Enabled bool   `json:"enabled"`
	Start   string `json:"start" pattern:"^([01]?[0-9]|2[0-3]):[0-5][0-9]$"`
	End     string `json:"end"   pattern:"^([01]?[0-9]|2[0-3]):[0-5][0-9]$"`
	// Days of week when quiet hours apply, 0 = Sun..6 = Sat. Empty = every day.
	Days    []int  `json:"days,omitempty"`
}

// AgentSchedule lets operators raise or lower the tick rate during a window.
// e.g. "every 60s during business hours, every 300s overnight".
type AgentSchedule struct {
	Name            string `json:"name"`
	Start           string `json:"start"            pattern:"^([01]?[0-9]|2[0-3]):[0-5][0-9]$"`
	End             string `json:"end"              pattern:"^([01]?[0-9]|2[0-3]):[0-5][0-9]$"`
	Days            []int  `json:"days,omitempty"`
	IntervalSeconds int    `json:"interval_seconds" minimum:"5" maximum:"3600"`
}

type AgentConfigEntry struct {
	ID          string      `json:"id"`
	Scope       string      `json:"scope"        enum:"global,group,host"`
	TargetID    string      `json:"target_id,omitempty"   doc:"NULL for global"`
	TargetName  string      `json:"target_name,omitempty" doc:"hostname for host-scoped, group name for group-scoped"`
	Config      AgentConfig `json:"config"`
	Description string      `json:"description,omitempty"`
	Enabled     bool        `json:"enabled"`
	CreatedAt   time.Time   `json:"created_at"`
	UpdatedAt   time.Time   `json:"updated_at"`
	UpdatedBy   string      `json:"updated_by,omitempty"`
}

type AgentConfigInput struct {
	Scope       string      `json:"scope"        enum:"global,group,host"`
	TargetID    string      `json:"target_id,omitempty" doc:"required for scope=group or host"`
	Config      AgentConfig `json:"config"`
	Description string      `json:"description,omitempty"`
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
