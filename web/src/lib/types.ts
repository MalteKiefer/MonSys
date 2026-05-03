// Mirrors of apitypes the UI consumes. Hand-typed for now; future iteration
// could code-gen from /docs/openapi.yaml so they stay in sync automatically.

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
};

export type Monitor = {
  id: string;
  type: "cert" | "postgres" | "mysql" | "mongodb" | "http" | "tcp";
  name: string;
  target: string;
  params?: Record<string, unknown>;
  interval_sec: number;
  enabled: boolean;
  created_at: string;
  created_by?: string;
  last_check_at?: string;
  last_status?: "ok" | "warn" | "fail" | "unknown";
  last_latency_ms?: number;
  last_detail?: string;
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
  delivery_errors: Record<string, string>;
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
