// Static catalogue of condition types, metric kinds, and category cards.
// Kept separate from the React components so it can be imported by helpers
// (e.g. preview-sentence renderers) without dragging JSX into them.

import {
  Activity,
  Bell,
  Box,
  ClipboardList,
  Mail,
  MessageCircle,
  MessageSquare,
  Package,
  Server,
  Shield,
} from "lucide-react";

import type { ChannelType, NotificationConditionType } from "../../../lib/types";

export const CONDITION_TYPES: Array<{
  value: NotificationConditionType;
  label: string;
  description: string;
}> = [
  { value: "host_offline", label: "Host offline", description: "Fires when liveness watcher classifies a host as offline." },
  { value: "monitor_failed", label: "Monitor failed", description: "Active probe (cert/http/tcp/db) reports non-OK." },
  { value: "cert_expiring", label: "Cert expiring", description: "TLS cert remaining days < threshold." },
  { value: "login_failed_threshold", label: "Login failed (threshold)", description: ">N failed web logins in M seconds." },
  { value: "security_updates_pending", label: "Security updates pending", description: "Host has >=N pending security updates." },
  { value: "metric_threshold", label: "Metric threshold", description: "CPU/RAM/Swap/Disk/Net/Workload — generic numeric threshold over a sliding window." },
  { value: "agent_outdated", label: "Agent outdated", description: "Host agent_version below baseline." },
  { value: "image_update_pending", label: "Container image update pending", description: "Workload update_available=true persists > N hours." },
  { value: "package_update_available", label: "Package updates (total)", description: "Total updates_count > threshold (non-security)." },
  { value: "pending_reboot", label: "Reboot pending", description: "Kernel package update installed but no reboot observed." },
  { value: "repo_metadata_stale", label: "Repo metadata stale", description: "Package repo metadata older than threshold." },
  { value: "login_anomaly", label: "Login anomaly", description: "Suspicious login: new source IP, root success, sudo spike." },
  { value: "inventory_drift", label: "Inventory drift", description: "Unexpected change: new user/sudoer/disk/NIC/MAC/kernel/distro." },
  { value: "firewall_state_change", label: "Firewall state change", description: "Firewall disabled, policy weakened, or rule count dropped." },
  { value: "fail2ban_jail_disappeared", label: "Fail2ban jail disappeared", description: "A previously-known jail is no longer reported." },
  { value: "crowdsec_decision_threshold", label: "CrowdSec decisions (threshold)", description: "Active decisions per host > threshold." },
  { value: "nic_link_down", label: "NIC link down", description: "Network interface transitions to 0 Mbps." },
  { value: "nic_bond_degraded", label: "NIC bond degraded", description: "Bond/bridge master members count below baseline." },
  { value: "vm_state_change", label: "VM state change", description: "VM transitions to stopped or autostart violation." },
  { value: "container_state_change", label: "Container state change", description: "Container transitions to exited/dead." },
  { value: "audit_action", label: "Audit action match", description: "New audit_log row matches actor/target/action filter." },
  { value: "host_flap", label: "Host flap", description: "Host status transitions > N in window." },
  { value: "unexpected_reboot", label: "Unexpected reboot", description: "uptime_sec dropped without pending_reboot resolve." },
];

// Category groupings used by the Step-1 picker. Each category maps to the set
// of condition_types reachable from that card.
export type CategoryKey =
  | "metrics"
  | "availability"
  | "updates"
  | "security"
  | "workloads"
  | "inventory";

export const CATEGORY_MAP: Record<CategoryKey, NotificationConditionType[]> = {
  metrics: ["metric_threshold"],
  availability: ["host_offline", "host_flap", "unexpected_reboot", "monitor_failed"],
  updates: [
    "security_updates_pending",
    "package_update_available",
    "pending_reboot",
    "repo_metadata_stale",
    "image_update_pending",
    "agent_outdated",
    "cert_expiring",
  ],
  security: [
    "login_failed_threshold",
    "login_anomaly",
    "firewall_state_change",
    "fail2ban_jail_disappeared",
    "crowdsec_decision_threshold",
    "audit_action",
  ],
  workloads: ["container_state_change", "vm_state_change", "nic_link_down", "nic_bond_degraded"],
  inventory: ["inventory_drift"],
};

export const CATEGORY_CARDS: Array<{
  key: CategoryKey;
  label: string;
  blurb: string;
  icon: typeof Activity;
}> = [
  { key: "metrics", label: "Metrics", blurb: "CPU, RAM, disk, network thresholds.", icon: Activity },
  { key: "availability", label: "Availability", blurb: "Host offline, flaps, monitors.", icon: Server },
  { key: "updates", label: "Updates", blurb: "Pkg updates, certs, reboots.", icon: Package },
  { key: "security", label: "Security", blurb: "Logins, firewall, fail2ban, audit.", icon: Shield },
  { key: "workloads", label: "Workloads", blurb: "Containers, VMs, NIC link state.", icon: Box },
  { key: "inventory", label: "Inventory", blurb: "New user, disk, NIC, kernel drift.", icon: ClipboardList },
];

export function categoryOf(ct: NotificationConditionType): CategoryKey | null {
  for (const k of Object.keys(CATEGORY_MAP) as CategoryKey[]) {
    if (CATEGORY_MAP[k].includes(ct)) return k;
  }
  return null;
}

// Friendly labels for MetricKind values. Order matches apitypes.MetricKind
// const block so dropdown ordering is stable. Each entry can advertise which
// optional `scope.*` keys make sense, so the metric_threshold pane can show
// the right sub-inputs.
export const METRIC_KINDS: Array<{
  value: string;
  label: string;
  scopeHint?: ("mountpoint" | "nic" | "workload_id" | "monitor_id")[];
}> = [
  { value: "cpu_usage_pct", label: "CPU usage %" },
  { value: "cpu_per_core_pct", label: "CPU per-core % (any core)" },
  { value: "load_1", label: "Load average (1 min)" },
  { value: "load_5", label: "Load average (5 min)" },
  { value: "load_15", label: "Load average (15 min)" },
  { value: "ram_used_pct", label: "RAM used %" },
  { value: "swap_used_pct", label: "Swap used %" },
  { value: "swap_used_bytes", label: "Swap used (bytes)" },
  { value: "disk_used_pct", label: "Disk used %", scopeHint: ["mountpoint"] },
  { value: "disk_inode_used_pct", label: "Disk inode used %", scopeHint: ["mountpoint"] },
  { value: "disk_iops_total", label: "Disk IOPS (total)", scopeHint: ["mountpoint"] },
  { value: "disk_io_util_pct", label: "Disk I/O utilisation %", scopeHint: ["mountpoint"] },
  { value: "nic_rx_bytes_per_sec", label: "NIC RX rate (bytes/s)", scopeHint: ["nic"] },
  { value: "nic_tx_bytes_per_sec", label: "NIC TX rate (bytes/s)", scopeHint: ["nic"] },
  { value: "nic_err_per_sec", label: "NIC errors/s", scopeHint: ["nic"] },
  { value: "nic_drop_per_sec", label: "NIC drops/s", scopeHint: ["nic"] },
  { value: "workload_cpu_usage_pct", label: "Workload CPU %", scopeHint: ["workload_id"] },
  { value: "workload_mem_used_pct", label: "Workload memory %", scopeHint: ["workload_id"] },
  { value: "fail2ban_currently_banned", label: "Fail2ban currently banned" },
  { value: "crowdsec_active_decisions", label: "CrowdSec active decisions" },
  { value: "repo_metadata_age_sec", label: "Repo metadata age (s)" },
  { value: "monitor_last_latency_ms", label: "Monitor latency (ms)", scopeHint: ["monitor_id"] },
];

// condition_types that take no parameters beyond targeting. The pane renders
// a muted explanatory note rather than empty space.
export const NO_PARAM_CONDITIONS = new Set<NotificationConditionType>([
  "host_offline",
  "monitor_failed",
  "pending_reboot",
  "fail2ban_jail_disappeared",
  "nic_bond_degraded",
  "unexpected_reboot",
]);

// Lucide icon picker for channel types. Keep in sync with CHANNEL_TYPE_CARDS
// in ChannelForm.tsx.
export function channelIcon(type: ChannelType) {
  switch (type) {
    case "email":
      return Mail;
    case "slack":
      return MessageSquare;
    case "mattermost":
    case "discord":
      return MessageCircle;
    case "ntfy":
      return Bell;
    default:
      return Bell;
  }
}
