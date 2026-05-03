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

export type LoginResponse = {
  token: string;
  expires_at: string;
  user: { id: string; email: string; role: string };
};
