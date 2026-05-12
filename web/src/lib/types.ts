// Mirrors of apitypes the UI consumes. Hand-typed for now; future iteration
// could code-gen from /docs/openapi.yaml so they stay in sync automatically.

export interface HostGroupRef { id: string; name: string }

export interface Host {
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
  pending_updates?: number;
  security_updates?: number;
}

export interface HostGroup {
  id: string;
  name: string;
  description?: string;
  created_at: string;
  created_by?: string;
  member_ids: string[];
}

export interface CurrentUser {
  id: string;
  email: string;
  role: string;
  totp_active: boolean;
  passkey_count?: number;
  must_enroll?: boolean;
  grace_until?: string | null;
  // True when the user has uploaded an avatar. The image bytes are served
  // by GET /v1/users/{id}/avatar; avatar_updated_at lets the client cache-
  // bust the URL after re-upload without hammering the server otherwise.
  has_avatar?: boolean;
  avatar_updated_at?: string | null;
  // Persisted UI locale ("en" | "de", or empty/undefined for "auto"
  // detection). The TopBar language switcher writes this through
  // POST /v1/auth/me/language so the choice survives across devices.
  language?: string;
}

// Passkey (WebAuthn credential) summary returned by /v1/auth/passkeys.
export interface Passkey {
  id: string;
  name: string;
  aaguid?: string;
  transports?: string[];
  backup_eligible: boolean;
  backup_state: boolean;
  created_at: string;
  last_used_at?: string | null;
}

export interface ListPasskeysResponse { passkeys: Passkey[] }

// Admin-managed security policy. force_mode controls whether 2FA / passkeys
// are required server-wide; grace_days is how long new users have to enroll
// before their account is gated. max_session_hours and idle_timeout_minutes
// bound the lifetime of issued tokens.
export type ForceMode = "off" | "2fa_any" | "passkey_required";
export interface SecurityPolicy {
  force_mode: ForceMode;
  grace_days: number;
  max_session_hours: number;
  idle_timeout_minutes: number;
}

export interface RevokeAllSessionsResponse { revoked: number }

// WebAuthn begin-step responses. `options` is the raw PublicKeyCredential*
// dict the browser feeds to navigator.credentials.create/get — keep it typed
// as `unknown` here; the webauthn.ts helper does the heavy lifting.
export interface WebAuthnRegisterBeginResponse {
  challenge_token: string;
  options: unknown;
}
export interface WebAuthnLoginBeginResponse {
  challenge_token: string;
  options: unknown;
}

// Server-wide auth/notification readiness flags surfaced to any logged-in
// user (mirrors apitypes.AuthConfig). Used by non-admins to gate UI hints
// without exposing admin-only settings.
export interface AuthConfig {
  sso_enabled: boolean;
  smtp_configured: boolean;
}

export interface LoginResponse {
  needs_totp: boolean;
  needs_passkey?: boolean;
  challenge_token?: string;
  token?: string;
  expires_at: string;
  user: CurrentUser;
}

export interface TOTPSetup {
  secret_b32: string;
  otpauth_url: string;
  qr_png_base64: string;
  backup_codes: string[];
}

export interface AdminUser {
  id: string;
  email: string;
  role: "admin" | "user";
  created_at: string;
  disabled_at?: string | null;
  totp_active: boolean;
  last_login_at?: string | null;
}

export interface PasswordPolicy {
  min_length: number;
  require_upper: boolean;
  require_lower: boolean;
  require_digit: boolean;
  require_symbol: boolean;
  max_age_days: number;
}

export interface AdminCreateUserResponse {
  user: AdminUser;
  reset_url?: string;
  invite_sent: boolean;
}

// Host detail bundles

export interface DiskRow {
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
}

export interface NicRow {
  id: string;
  name: string;
  mac: string;
  speed_mbps: number;
  addrs: string[];
  members: string[];
  bridge_master?: string;
  last_seen_at: string;
  latest_time?: string;
  rx_bytes: number;
  tx_bytes: number;
}

export interface WorkloadRow {
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
  // Image-update detection (Docker only for now). The agent compares the
  // running container's image digest against the upstream registry's digest
  // for the same image:tag and reports the verdict here. Empty strings mean
  // "not yet checked" or "couldn't reach the registry" — render the badge
  // only when update_available is true.
  current_digest?: string;
  latest_digest?: string;
  update_available?: boolean;
  update_checked_at?: string;
}

export interface VMRow {
  kind: string;
  external_id: string;
  name: string;
  state: string;
  vcpu: number;
  mem_bytes: number;
  autostart: boolean;
  last_seen_at: string;
}

export interface ObservedUser {
  username: string;
  uid: number;
  gid: number;
  shell?: string;
  home?: string;
  is_sudoer: boolean;
  is_system: boolean;
  last_login_at?: string | null;
  last_seen_at: string;
}

export interface PackageSummary {
  time: string;
  installed_count: number;
  updates_count: number;
  security_updates: number;
  metadata_age_seconds: number;
}

export interface RepoMetaState {
  manager: string;
  metadata_mtime: string;
  metadata_age_seconds: number;
  refreshed_externally: boolean;
}

export interface HostDetail {
  host: Host;
  disks: DiskRow[];
  nics: NicRow[];
  workloads: WorkloadRow[];
  vms: VMRow[];
  users: ObservedUser[];
  packages_summary?: PackageSummary;
  repo_states: RepoMetaState[];
}

export interface FirewallStatus {
  engine: string;
  active: boolean;
  default_input?: string;
  default_output?: string;
  default_forward?: string;
  rule_count: number;
  snapshot_excerpt?: string;
}

export interface Fail2banJailInfo {
  jail: string;
  currently_failed: number;
  total_failed: number;
  currently_banned: number;
  total_banned: number;
  banned_ips?: string[];
}

export interface CrowdsecDecision {
  decision_id: string;
  origin?: string;
  scope?: string;
  target?: string;
  type?: string;
  reason?: string;
  until?: string;
}

export interface HostSecurity {
  host_id: string;
  firewalls: FirewallStatus[];
  fail2ban: Fail2banJailInfo[] | null;
  crowdsec: CrowdsecDecision[] | null;
}

export interface LoginEvent {
  time: string;
  username?: string;
  source_ip?: string;
  method: string;
  success: boolean;
  detail?: string;
}

export interface SystemSample {
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
}

// Notifications

export type ChannelType = "email" | "slack" | "mattermost" | "discord" | "ntfy";

export interface NotificationChannel {
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
}

export interface NotificationChannelInput {
  type: ChannelType;
  name: string;
  enabled: boolean;
  config?: Record<string, unknown>;
  recipient_email?: string;
}

export interface SmtpSettings {
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
}

export interface NotificationSettings {
  quiet_enabled: boolean;
  quiet_start: string;
  quiet_end: string;
  quiet_days: number[];
  quiet_tz: string;
  updated_at: string;
  updated_by: string;
}

export interface NotificationSettingsInput {
  quiet_enabled: boolean;
  quiet_start: string;
  quiet_end: string;
  quiet_days: number[];
  quiet_tz: string;
}

export interface SmtpSettingsInput {
  host: string;
  port: number;
  username?: string;
  password?: string;
  clear_password?: boolean;
  from_address: string;
  starttls: boolean;
  tls: boolean;
  insecure_skip_verify: boolean;
}

export type NotificationConditionType =
  | "host_offline"
  | "monitor_failed"
  | "cert_expiring"
  | "login_failed_threshold"
  | "security_updates_pending"
  | "metric_threshold"
  | "agent_outdated"
  | "image_update_pending"
  | "package_update_available"
  | "pending_reboot"
  | "repo_metadata_stale"
  | "login_anomaly"
  | "inventory_drift"
  | "firewall_state_change"
  | "fail2ban_jail_disappeared"
  | "crowdsec_decision_threshold"
  | "nic_link_down"
  | "nic_bond_degraded"
  | "vm_state_change"
  | "container_state_change"
  | "audit_action"
  | "host_flap"
  | "unexpected_reboot";

export interface NotificationRule {
  id: string;
  name: string;
  enabled: boolean;
  condition_type: NotificationConditionType;
  condition_params?: Record<string, unknown>;
  channel_ids: string[];
  severity: "info" | "warning" | "critical";
  throttle_sec: number;
  repeat_interval_sec: number;
  notify_on_resolve: boolean;
  target_host_ids: string[];
  target_tags: string[];
  target_group_ids: string[];
  // Set when this rule is one leg of a multi-condition group. Rows with the
  // same group_id share name/scope/channels/throttle/severity; only their
  // condition_type+condition_params differ.
  group_id?: string | null;
  created_at: string;
  created_by?: string;
}

export interface NotificationRuleInput {
  name: string;
  enabled: boolean;
  condition_type: NotificationRule["condition_type"];
  condition_params?: Record<string, unknown>;
  channel_ids: string[];
  severity: NotificationRule["severity"];
  throttle_sec: number;
  repeat_interval_sec: number;
  notify_on_resolve: boolean;
  target_host_ids?: string[];
  target_tags?: string[];
  target_group_ids?: string[];
}

// One leg of a multi-condition group. The shared (name/scope/channels/…)
// fields live on NotificationRuleGroupInput; only condition_type and
// condition_params differ per leg.
export interface NotificationRuleCondition {
  condition_type: NotificationConditionType;
  condition_params?: Record<string, unknown>;
}

// Body shape for POST /v1/notifications/rules/batch. Mirrors the Go
// NotificationRuleGroupInput. The backend expands `conditions` into N rule
// rows that all share the same group_id and (when len > 1) get a per-row
// " — <condition_type>" suffix appended to `name`.
export interface NotificationRuleGroupInput {
  name: string;
  enabled: boolean;
  severity: NotificationRule["severity"];
  throttle_sec: number;
  repeat_interval_sec: number;
  notify_on_resolve: boolean;
  channel_ids: string[];
  conditions: NotificationRuleCondition[];
  target_host_ids?: string[];
  target_tags?: string[];
  target_group_ids?: string[];
  // When set, server atomically deletes these rule ids before inserting the
  // new batch, all in one tx. Used by the wizard for edit / single→multi.
  replace_existing_ids?: string[];
}

export interface NotificationRuleGroupResponse {
  group_id: string;
  rules: NotificationRule[];
}

export interface AlertHistoryEntry {
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
}

// Monitors

export interface Monitor {
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
}

export interface MonitorInput {
  type: Monitor["type"];
  name: string;
  target: string;
  params?: Record<string, unknown>;
  interval_sec: number;
  enabled: boolean;
  target_tags?: string[];
  target_group_ids?: string[];
}

export interface MonitorResult {
  time: string;
  status: "ok" | "warn" | "fail" | "unknown";
  latency_ms: number;
  detail?: string;
}

export interface DiskSample {
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
}

export interface NetSample {
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
}

export interface GlobalPackageRow {
  host_id: string;
  hostname: string;
  manager: string;
  name: string;
  version: string;
  arch?: string;
  source_repo?: string;
  installed_at?: string;
}

export interface IngestSummary {
  idx: number;
  time: string;
  host_id: string;
  hostname?: string;
  size_bytes: number;
}

export interface IngestPayload {
  time: string;
  host_id: string;
  hostname?: string;
  size_bytes: number;
  truncated: boolean;
  payload: unknown;
}

// Agent configuration

export interface AgentPackagesConfig {
  enabled?: boolean;
  update_check_interval?: string;
  full_snapshot_max_interval?: string;
}

export interface AgentQuietHours {
  enabled: boolean;
  start: string;
  end: string;
  days?: number[];
}

export interface AgentSchedule {
  name: string;
  start: string;
  end: string;
  days?: number[];
  interval_seconds: number;
}

export interface AgentConfig {
  interval_seconds?: number;
  buffer_max_mb?: number;
  packages?: AgentPackagesConfig;
  quiet_hours?: AgentQuietHours;
  schedules?: AgentSchedule[];
  labels?: Record<string, string>;
}

export interface AgentConfigEntry {
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
}

export interface AgentConfigInput {
  scope: "global" | "group" | "host";
  target_id?: string;
  config: AgentConfig;
  description?: string;
  enabled: boolean;
}

export interface AgentConfigResolved {
  config: AgentConfig;
  source_scopes: string[];
  fetched_at: string;
}

export interface ServerLogEntry {
  time: string;
  level: "DEBUG" | "INFO" | "WARN" | "ERROR";
  msg: string;
  attrs?: Record<string, unknown>;
}

export interface PendingUpdate {
  manager: string;
  name: string;
  arch?: string;
  current_version: string;
  available_version: string;
  source_repo?: string;
  is_security: boolean;
}

export interface AuditEntry {
  id: number;
  actor: string;
  action: string;
  target: string;
  detail: string;
  at: string;
}

// Agent self-enrollment. An admin issues a short-lived enrollment token bound
// to optional defaults (label/tags/groups), and a fresh agent installs itself
// via `curl /v1/agents/install.sh?t=<token> | sh`. The token is single-use and
// the server records which host claimed it (`used_*`).

export interface AgentEnrollment {
  id: string;
  label?: string;
  description?: string;
  tags: string[];
  group_ids: string[];
  expires_at: string;
  created_at: string;
  created_by?: string;
  used_at?: string;
  used_by_host_id?: string;
  used_by_hostname?: string;
}

export interface AgentEnrollmentInput {
  label?: string;
  description?: string;
  tags?: string[];
  group_ids?: string[];
  ttl_minutes?: number;
}

export interface AgentEnrollmentCreateResponse {
  enrollment: AgentEnrollment;
  token: string;
  install_command: string;
  install_url: string;
}
