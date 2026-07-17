// Static catalogue of condition types, metric kinds, and category cards.
// Kept separate from the React components so it can be imported by helpers
// (e.g. preview-sentence renderers) without dragging JSX into them.

import {
  Activity,
  AlertTriangle,
  Bell,
  Box,
  ClipboardList,
  FileWarning,
  Gauge,
  KeyRound,
  Mail,
  MessageCircle,
  MessageSquare,
  Network,
  Package,
  RefreshCcw,
  RotateCw,
  ScrollText,
  Server,
  ServerCog,
  Shield,
  ShieldAlert,
  ShieldOff,
} from "lucide-react";

import type { ChannelType, NotificationConditionType } from "../../../lib/types";
import { asNumberOrEmpty, asString, asStringArray, type Params } from "./coerce";

export const CONDITION_TYPES: {
  value: NotificationConditionType;
  label: string;
  description: string;
}[] = [
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
  { value: "mail_service_down", label: "Mail service down", description: "A mail daemon (Postfix, Dovecot, …) is no longer active on the host." },
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
  availability: ["host_offline", "host_flap", "unexpected_reboot", "monitor_failed", "mail_service_down"],
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

export const CATEGORY_CARDS: {
  key: CategoryKey;
  label: string;
  blurb: string;
  icon: typeof Activity;
}[] = [
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
export const METRIC_KINDS: {
  value: string;
  label: string;
  scopeHint?: ("mountpoint" | "nic" | "workload_id" | "monitor_id")[];
}[] = [
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
  { value: "mail_queue_deferred", label: "Mail queue deferred (count)" },
  { value: "mail_queue_total", label: "Mail queue total (count)" },
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
  "mail_service_down",
]);

// Per-condition-type icon for the condition list card in Step 1 and for the
// grouped-rule legs in RulesPage. Falls back to a neutral Bell.
export function conditionIcon(ct: NotificationConditionType) {
  switch (ct) {
    case "metric_threshold":
      return Gauge;
    case "host_offline":
    case "host_flap":
    case "unexpected_reboot":
      return Server;
    case "monitor_failed":
      return Activity;
    case "cert_expiring":
      return FileWarning;
    case "login_failed_threshold":
    case "login_anomaly":
      return KeyRound;
    case "security_updates_pending":
    case "package_update_available":
    case "image_update_pending":
    case "agent_outdated":
      return Package;
    case "pending_reboot":
      return RotateCw;
    case "repo_metadata_stale":
      return RefreshCcw;
    case "firewall_state_change":
      return ShieldOff;
    case "fail2ban_jail_disappeared":
      return ShieldAlert;
    case "crowdsec_decision_threshold":
      return Shield;
    case "nic_link_down":
    case "nic_bond_degraded":
      return Network;
    case "vm_state_change":
      return ServerCog;
    case "container_state_change":
      return Box;
    case "audit_action":
      return ScrollText;
    case "inventory_drift":
      return ClipboardList;
    case "mail_service_down":
      return Mail;
    default:
      return AlertTriangle;
  }
}

// Friendly label for a condition type (the same one shown in the picker).
// Used for collapsed-card titles and the grouped-list legs in RulesPage.
export function conditionLabel(ct: NotificationConditionType): string {
  return CONDITION_TYPES.find((c) => c.value === ct)?.label ?? ct;
}

// conditionSummary renders the shared "X > Y for Z" 1-line sentence used by:
//   • LivePreview (multi-condition bullet list)
//   • StepDetect collapsed condition cards
//   • RulesPage grouped-rule legs
//
// Returns plain text only — no JSX — so it can be used in both <p> and
// <strong>-wrapped contexts. The caller is responsible for highlighting.
export function conditionSummary(
  ct: NotificationConditionType,
  params: Params,
): string {
  switch (ct) {
    case "metric_threshold": {
      const metric = asString(params.metric, "cpu_usage_pct");
      const comparator = asString(params.comparator, ">");
      const value = asNumberOrEmpty(params.value);
      const win = asNumberOrEmpty(params.window_sec);
      const metricLabel = METRIC_KINDS.find((m) => m.value === metric)?.label ?? metric;
      const v = value === "" ? "?" : String(value);
      let s = `${metricLabel} ${comparator} ${v}`;
      if (win !== "") {
        const minutes = Math.round((win) / 60);
        s += ` for ${minutes >= 1 ? `${minutes} min` : `${win}s`}`;
      }
      return s;
    }
    case "host_offline":
      return "Host goes offline for the configured liveness window";
    case "host_flap": {
      const win = asNumberOrEmpty(params.window_sec);
      const thr = asNumberOrEmpty(params.threshold);
      const winSec = win === "" ? 1800 : (win);
      const minutes = Math.round(winSec / 60);
      const t = thr === "" ? 6 : (thr);
      return `Host flaps > ${t} times within ${minutes} min`;
    }
    case "unexpected_reboot":
      return "Host reboots unexpectedly";
    case "monitor_failed":
      return "A monitor reports a non-OK status";
    case "cert_expiring": {
      const days = asNumberOrEmpty(params.days_threshold);
      return `Certificate expires within ${days === "" ? 30 : days} days`;
    }
    case "login_failed_threshold": {
      const thr = asNumberOrEmpty(params.threshold);
      const win = asNumberOrEmpty(params.window_sec);
      return `More than ${thr === "" ? 10 : thr} failed logins in ${win === "" ? 300 : win}s`;
    }
    case "security_updates_pending": {
      const thr = asNumberOrEmpty(params.threshold);
      return `Host has ≥ ${thr === "" ? 1 : thr} pending security updates`;
    }
    case "agent_outdated": {
      const min = asString(params.min_version);
      return `Agent version below ${min || "the latest seen"}`;
    }
    case "image_update_pending": {
      const h = asNumberOrEmpty(params.min_age_hours);
      return `Container image update pending older than ${h === "" ? 24 : h}h`;
    }
    case "package_update_available": {
      const thr = asNumberOrEmpty(params.threshold);
      return `Host has more than ${thr === "" ? 50 : thr} pending package updates`;
    }
    case "pending_reboot":
      return "Host has a pending reboot";
    case "repo_metadata_stale": {
      const s = asNumberOrEmpty(params.threshold_sec);
      const secs = s === "" ? 86400 : (s);
      const hours = Math.round(secs / 3600);
      return `Repository metadata older than ${hours}h`;
    }
    case "login_anomaly":
      return `Login anomaly (${asString(params.kind, "new_source_ip")})`;
    case "inventory_drift":
      return `Inventory drift (${asString(params.kind, "new_user")})`;
    case "firewall_state_change":
      return `Firewall state: ${asString(params.kind, "inactive")}`;
    case "fail2ban_jail_disappeared":
      return "A previously-known fail2ban jail disappears";
    case "crowdsec_decision_threshold": {
      const thr = asNumberOrEmpty(params.threshold);
      return `CrowdSec active decisions > ${thr === "" ? 100 : thr}`;
    }
    case "nic_link_down":
      return "A NIC link goes down";
    case "nic_bond_degraded":
      return "A NIC bond is degraded";
    case "vm_state_change":
      return `VM transition (${asString(params.subkind, "any_transition")})`;
    case "container_state_change": {
      const states = asStringArray(params.states);
      const shown = states.length > 0 ? states.join(", ") : "exited, dead";
      return `Container enters one of: ${shown}`;
    }
    case "audit_action": {
      const actions = asStringArray(params.actions);
      return `Audit action matches: ${actions.length > 0 ? actions.join(", ") : "—"}`;
    }
    case "mail_service_down":
      return "A mail service is no longer active";
    default:
      return ct;
  }
}

// Stripping the auto-appended " — <condition_type>" suffix the backend adds
// when a group has more than one leg. Used by RulesPage so the grouped card
// header shows the operator-chosen name only once.
const SUFFIX_RE = / — [a-z_]+$/i;
export function stripConditionSuffix(name: string): string {
  return name.replace(SUFFIX_RE, "");
}

// Per-condition validation used by isStep1Valid for both single and
// multi-condition drafts. Returns true if this leg has the minimum required
// params filled in (most types have sensible defaults and are always valid).
export function isConditionValid(
  ct: NotificationConditionType,
  params: Params,
): boolean {
  if (ct === "metric_threshold") {
    const v = params.value;
    if (v === undefined || v === null || v === "") return false;
  }
  if (ct === "audit_action") {
    const actions = asStringArray(params.actions);
    if (actions.length === 0) return false;
  }
  return true;
}

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
