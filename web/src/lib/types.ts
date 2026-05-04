// Mirrors of apitypes the UI consumes. Hand-typed for now; future iteration
// could code-gen from /docs/openapi.yaml so they stay in sync automatically.

export type HostGroupRef = { id: string; name: string };

export type Host = {
  id: string;
  hostname: string;
  distro: string;
  arch: string;
  cpu_cores: number;
  ram_total_bytes: number;
  agent_version: string;
  first_seen_at: string;
  last_seen_at: string;
  status: "online" | "stale" | "offline" | "unknown";
  status_since?: string;
  labels: Record<string, string>;
  tags: string[];
  groups: HostGroupRef[];
  distro_family?: string;
  services?: string[];
};

export type HostGroup = {
  id: string;
  name: string;
  description?: string;
  created_at: string;
  created_by?: string;
  member_ids: string[];
};

export type CurrentUser = {
  id: string;
  email: string;
  role: string;
  totp_active: boolean;
};

export type LoginResponse = {
  needs_totp: boolean;
  challenge_token?: string;
  token?: string;
  expires_at: string;
  user: CurrentUser;
};

export type TOTPSetup = {
  secret_b32: string;
  otpauth_url: string;
  qr_png_base64: string;
  backup_codes: string[];
};

export type AdminUser = {
  id: string;
  email: string;
  role: "admin" | "user";
  created_at: string;
  disabled_at?: string | null;
  totp_active: boolean;
  last_login_at?: string | null;
};

export type PasswordPolicy = {
  min_length: number;
  require_upper: boolean;
  require_lower: boolean;
  require_digit: boolean;
  require_symbol: boolean;
  max_age_days: number;
};

export type AdminCreateUserResponse = {
  user: AdminUser;
  reset_url?: string;
  invite_sent: boolean;
};

// Host detail bundles

export type DiskRow = {
  id: string;
  device: string;
  mountpoint: string;
  fstype: string;
  size_bytes: number;
  is_removable: boolean;
  last_seen_at: string;
  latest_time?: string;
  used_bytes: number;
  free_bytes: number;
};

export type NicRow = {
  id: string;
  name: string;
  mac: string;
  speed_mbps: number;
  last_seen_at: string;
  latest_time?: string;
  rx_bytes: number;
  tx_bytes: number;
};

export type WorkloadRow = {
  id: string;
  kind: string;
  external_id: string;
  name: string;
  image?: string;
  state: string;
  labels?: Record<string, string>;
  last_seen_at: string;
  latest_time?: string;
  cpu_usage_pct: number;
  mem_used_bytes: number;
};

export type VMRow = {
  kind: string;
  external_id: string;
  name: string;
  state: string;
  vcpu: number;
  mem_bytes: number;
  autostart: boolean;
  last_seen_at: string;
};

export type ObservedUser = {
  username: string;
  uid: number;
  gid: number;
  shell?: string;
  home?: string;
  is_sudoer: boolean;
  is_system: boolean;
  last_login_at?: string | null;
  last_seen_at: string;
};

export type PackageSummary = {
  time: string;
  installed_count: number;
  updates_count: number;
  security_updates: number;
  metadata_age_seconds: number;
};

export type RepoMetaState = {
  manager: string;
  metadata_mtime: string;
  metadata_age_seconds: number;
  refreshed_externally: boolean;
};

export type HostDetail = {
  host: Host;
  disks: DiskRow[];
  nics: NicRow[];
  workloads: WorkloadRow[];
  vms: VMRow[];
  users: ObservedUser[];
  packages_summary?: PackageSummary;
  repo_states: RepoMetaState[];
};

export type FirewallStatus = {
  engine: string;
  active: boolean;
  default_input?: string;
  default_output?: string;
  default_forward?: string;
  rule_count: number;
  snapshot_excerpt?: string;
};

export type Fail2banJailInfo = {
  jail: string;
  currently_failed: number;
  total_failed: number;
  currently_banned: number;
  total_banned: number;
  banned_ips?: string[];
};

export type CrowdsecDecision = {
  decision_id: string;
  origin?: string;
  scope?: string;
  target?: string;
  type?: string;
  reason?: string;
  until?: string;
};

export type HostSecurity = {
  host_id: string;
  firewalls: FirewallStatus[];
  fail2ban: Fail2banJailInfo[] | null;
  crowdsec: CrowdsecDecision[] | null;
};

export type LoginEvent = {
  time: string;
  username?: string;
  source_ip?: string;
  method: string;
  success: boolean;
  detail?: string;
};

export type SystemSample = {
  time: string;
  cpu_usage_pct: number;
  cpu_per_core?: number[];
  load_1: number;
  load_5: number;
  load_15: number;
  ram_used_bytes: number;
  ram_avail_bytes: number;
  swap_used_bytes: number;
  uptime_sec: number;
};

// Notifications

export type ChannelType = "email" | "slack" | "mattermost" | "discord" | "ntfy";

export type NotificationChannel = {
  id: string;
  type: ChannelType;
  name: string;
  enabled: boolean;
  config: Record<string, unknown>;
  recipient_email?: string;
  created_at: string;
  created_by?: string;
  owner_user_id?: string;
  last_used_at?: string | null;
  last_error?: string;
};

export type NotificationChannelInput = {
  type: ChannelType;
  name: string;
  enabled: boolean;
  config?: Record<string, unknown>;
  recipient_email?: string;
};

export type SmtpSettings = {
  host: string;
  port: number;
  username: string;
  has_password: boolean;
  from_address: string;
  starttls: boolean;
  tls: boolean;
  insecure_skip_verify: boolean;
  updated_at: string;
  updated_by: string;
};

export type NotificationSettings = {
  quiet_enabled: boolean;
  quiet_start: string;
  quiet_end: string;
  quiet_days: number[];
  quiet_tz: string;
  updated_at: string;
  updated_by: string;
};

export type NotificationSettingsInput = {
  quiet_enabled: boolean;
  quiet_start: string;
  quiet_end: string;
  quiet_days: number[];
  quiet_tz: string;
};

export type SmtpSettingsInput = {
  host: string;
  port: number;
  username?: string;
  password?: string;
  clear_password?: boolean;
  from_address: string;
  starttls: boolean;
  tls: boolean;
  insecure_skip_verify: boolean;
};

export type NotificationRule = {
  id: string;
  name: string;
  enabled: boolean;
  condition_type:
    | "host_offline"
    | "monitor_failed"
    | "cert_expiring"
    | "login_failed_threshold"
    | "security_updates_pending";
  condition_params?: Record<string, unknown>;
  channel_ids: string[];
  severity: "info" | "warning" | "critical";
  throttle_sec: number;
  created_at: string;
  created_by?: string;
};

export type NotificationRuleInput = {
  name: string;
  enabled: boolean;
  condition_type: NotificationRule["condition_type"];
  condition_params?: Record<string, unknown>;
  channel_ids: string[];
  severity: NotificationRule["severity"];
  throttle_sec: number;
};

export type AlertHistoryEntry = {
  id: number;
  at: string;
  rule_id?: string;
  rule_name: string;
  severity: "info" | "warning" | "critical";
  subject: string;
  body: string;
  dedup_key: string;
  delivered_to: string[];
  delivery_errors: Record<string, unknown>;
};

// Monitors

export type Monitor = {
  id: string;
  type: "cert" | "postgres" | "mysql" | "mongodb" | "http" | "tcp";
  name: string;
  target: string;
  params?: Record<string, unknown>;
  interval_sec: number;
  enabled: boolean;
  target_tags: string[];
  target_group_ids: string[];
  created_at: string;
  created_by?: string;
  last_check_at?: string | null;
  last_status?: "ok" | "warn" | "fail" | "unknown";
  last_latency_ms?: number;
  last_detail?: string;
};

export type MonitorInput = {
  type: Monitor["type"];
  name: string;
  target: string;
  params?: Record<string, unknown>;
  interval_sec: number;
  enabled: boolean;
  target_tags?: string[];
  target_group_ids?: string[];
};

export type MonitorResult = {
  time: string;
  status: "ok" | "warn" | "fail" | "unknown";
  latency_ms: number;
  detail?: string;
};

export type DiskSample = {
  time: string;
  device: string;
  mountpoint: string;
  used_bytes: number;
  free_bytes: number;
  inodes_used: number;
  inodes_free: number;
  read_bytes: number;
  write_bytes: number;
  read_ops: number;
  write_ops: number;
  io_time_ms: number;
};

export type NetSample = {
  time: string;
  nic_name: string;
  rx_bytes: number;
  tx_bytes: number;
  rx_pkts: number;
  tx_pkts: number;
  rx_errs: number;
  tx_errs: number;
  rx_drops: number;
  tx_drops: number;
};

export type GlobalPackageRow = {
  host_id: string;
  hostname: string;
  manager: string;
  name: string;
  version: string;
  arch?: string;
  source_repo?: string;
  installed_at?: string;
};

export type IngestSummary = {
  idx: number;
  time: string;
  host_id: string;
  hostname?: string;
  size_bytes: number;
};

export type IngestPayload = {
  time: string;
  host_id: string;
  hostname?: string;
  size_bytes: number;
  truncated: boolean;
  payload: unknown;
};

// Agent configuration

export type AgentPackagesConfig = {
  enabled?: boolean;
  update_check_interval?: string;
  full_snapshot_max_interval?: string;
};

export type AgentQuietHours = {
  enabled: boolean;
  start: string;
  end: string;
  days?: number[];
};

export type AgentSchedule = {
  name: string;
  start: string;
  end: string;
  days?: number[];
  interval_seconds: number;
};

export type AgentConfig = {
  interval_seconds?: number;
  buffer_max_mb?: number;
  packages?: AgentPackagesConfig;
  quiet_hours?: AgentQuietHours;
  schedules?: AgentSchedule[];
  labels?: Record<string, string>;
};

export type AgentConfigEntry = {
  id: string;
  scope: "global" | "group" | "host";
  target_id?: string;
  target_name?: string;
  config: AgentConfig;
  description?: string;
  enabled: boolean;
  created_at: string;
  updated_at: string;
  updated_by?: string;
};

export type AgentConfigInput = {
  scope: "global" | "group" | "host";
  target_id?: string;
  config: AgentConfig;
  description?: string;
  enabled: boolean;
};

export type AgentConfigResolved = {
  config: AgentConfig;
  source_scopes: string[];
  fetched_at: string;
};

export type ServerLogEntry = {
  time: string;
  level: "DEBUG" | "INFO" | "WARN" | "ERROR";
  msg: string;
  attrs?: Record<string, unknown>;
};

export type PendingUpdate = {
  manager: string;
  name: string;
  arch?: string;
  current_version: string;
  available_version: string;
  source_repo?: string;
  is_security: boolean;
};
