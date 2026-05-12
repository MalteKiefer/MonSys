import { useMutation, useQuery } from "@tanstack/react-query";
import {
  Bell,
  Code2,
  Mail,
  MessageCircle,
  MessageSquare,
  Server,
  Sliders,
  Users,
} from "lucide-react";
import { FormEvent, useEffect, useMemo, useState } from "react";

import {
  Button,
  ErrorBox,
  Field,
  Panel,
  PanelBody,
  PanelHeader,
  TextInput,
} from "../../components/ui";
import {
  CheckboxGrid,
  FormSection,
  PillGroup,
  TagChip,
  Toggle,
} from "../../components/notifications/FormParts";
import { api, ApiError } from "../../lib/api";
import { hostDisplay } from "../../lib/utils";
import {
  ChannelType,
  Host,
  HostGroup,
  NotificationChannel,
  NotificationConditionType,
  NotificationRule,
  NotificationRuleInput,
} from "../../lib/types";

// ---- Rule form ------------------------------------------------------------
//
// Drives /v1/notifications/rules CRUD. The form exposes two modes:
//   * "Clicky-bunti": per-condition-type pane that renders typed inputs and
//     mutates a `condition_params` object.
//   * "Expert (JSON)": a raw JSON editor for the same object.
//
// `condition_params` is the single source of truth in component state; both
// modes read and write the same object. Switching modes preserves the data
// (best-effort: unknown JSON keys are silently ignored by the typed pane).

// ---------------------------------------------------------------------------
// Catalogue
// ---------------------------------------------------------------------------

const CONDITION_TYPES: Array<{
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

// Friendly labels for MetricKind values. Order matches apitypes.MetricKind
// const block so dropdown ordering is stable. Includes a per-metric hint of
// which optional `scope.*` keys make sense.
const METRIC_KINDS: Array<{
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
const NO_PARAM_CONDITIONS = new Set<NotificationConditionType>([
  "host_offline",
  "monitor_failed",
  "pending_reboot",
  "fail2ban_jail_disappeared",
  "nic_bond_degraded",
  "unexpected_reboot",
]);

// Lucide icon picker for channel types. Keep in sync with CHANNEL_TYPE_CARDS
// in ChannelForm.tsx.
function channelIcon(type: ChannelType) {
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

// ---------------------------------------------------------------------------
// Small shared inputs
// ---------------------------------------------------------------------------

type Params = Record<string, unknown>;

function NumberField({
  label,
  hint,
  value,
  onChange,
  min,
  step,
  placeholder,
}: {
  label: string;
  hint?: string;
  value: number | "";
  onChange: (v: number | "") => void;
  min?: number;
  step?: number;
  placeholder?: string;
}) {
  return (
    <Field label={label} hint={hint}>
      <TextInput
        type="number"
        min={min}
        step={step}
        value={value === "" ? "" : String(value)}
        placeholder={placeholder}
        onChange={(e) => {
          const raw = e.target.value;
          if (raw === "") {
            onChange("");
            return;
          }
          const n = Number(raw);
          onChange(Number.isFinite(n) ? n : "");
        }}
      />
    </Field>
  );
}

function SelectField<T extends string>({
  label,
  hint,
  value,
  onChange,
  options,
}: {
  label: string;
  hint?: string;
  value: T;
  onChange: (v: T) => void;
  options: Array<{ value: T; label: string }>;
}) {
  return (
    <Field label={label} hint={hint}>
      <select
        value={value}
        onChange={(e) => onChange(e.target.value as T)}
        className="w-full rounded-md border border-border bg-panel px-3 py-2 text-sm focus:border-accent focus:outline-none"
      >
        {options.map((o) => (
          <option key={o.value} value={o.value}>
            {o.label}
          </option>
        ))}
      </select>
    </Field>
  );
}

// Helpers for safe value extraction. We accept anything from the JSON editor
// so the typed inputs must defensively coerce or fall back to sensible
// defaults rather than blowing up on bad data.
function asString(v: unknown, fallback = ""): string {
  return typeof v === "string" ? v : fallback;
}
function asNumberOrEmpty(v: unknown): number | "" {
  if (v === undefined || v === null || v === "") return "";
  if (typeof v === "number" && Number.isFinite(v)) return v;
  if (typeof v === "string") {
    const n = Number(v);
    if (Number.isFinite(n)) return n;
  }
  return "";
}
function asBool(v: unknown, fallback = false): boolean {
  return typeof v === "boolean" ? v : fallback;
}
function asStringArray(v: unknown): string[] {
  if (Array.isArray(v)) {
    return v.filter((x): x is string => typeof x === "string");
  }
  return [];
}
function asRecord(v: unknown): Record<string, unknown> {
  if (v && typeof v === "object" && !Array.isArray(v)) {
    return v as Record<string, unknown>;
  }
  return {};
}

// ---------------------------------------------------------------------------
// Per-type panes
// ---------------------------------------------------------------------------

type PaneProps = {
  params: Params;
  setParams: (next: Params) => void;
};

function NoParamsPane() {
  return (
    <p className="rounded-md border border-dashed border-border bg-panel-2/30 px-3 py-3 text-xs text-fg-subtle">
      Keine zusätzlichen Parameter — Targeting unten konfigurieren.
    </p>
  );
}

function CertExpiringPane({ params, setParams }: PaneProps) {
  const days = asNumberOrEmpty(params.days_threshold);
  return (
    <div className="grid grid-cols-1 gap-3 md:grid-cols-2">
      <NumberField
        label="Days threshold"
        hint="Fire when remaining cert days < this. Default 30."
        value={days === "" ? 30 : days}
        min={1}
        onChange={(v) => setParams({ ...params, days_threshold: v === "" ? 30 : v })}
      />
    </div>
  );
}

function LoginFailedThresholdPane({ params, setParams }: PaneProps) {
  const win = asNumberOrEmpty(params.window_sec);
  const thr = asNumberOrEmpty(params.threshold);
  return (
    <div className="grid grid-cols-1 gap-3 md:grid-cols-2">
      <NumberField
        label="Window (seconds)"
        hint="Default 300."
        value={win === "" ? 300 : win}
        min={1}
        onChange={(v) => setParams({ ...params, window_sec: v === "" ? 300 : v })}
      />
      <NumberField
        label="Threshold"
        hint="Default 10."
        value={thr === "" ? 10 : thr}
        min={1}
        onChange={(v) => setParams({ ...params, threshold: v === "" ? 10 : v })}
      />
    </div>
  );
}

function SecurityUpdatesPendingPane({ params, setParams }: PaneProps) {
  const thr = asNumberOrEmpty(params.threshold);
  return (
    <div className="grid grid-cols-1 gap-3 md:grid-cols-2">
      <NumberField
        label="Threshold"
        hint="Fire when security_updates >= threshold. Default 1."
        value={thr === "" ? 1 : thr}
        min={1}
        onChange={(v) => setParams({ ...params, threshold: v === "" ? 1 : v })}
      />
    </div>
  );
}

function MetricThresholdPane({ params, setParams }: PaneProps) {
  const metric = asString(params.metric, "cpu_usage_pct");
  const comparator = asString(params.comparator, ">");
  const value = asNumberOrEmpty(params.value);
  const windowSec = asNumberOrEmpty(params.window_sec);
  const forSec = asNumberOrEmpty(params.for_sec);
  const scope = asRecord(params.scope);
  const metricMeta = METRIC_KINDS.find((m) => m.value === metric);
  const scopeKeys = metricMeta?.scopeHint ?? [];

  function patchScope(key: string, raw: string) {
    const next = { ...scope };
    if (raw.trim() === "") {
      delete next[key];
    } else {
      next[key] = raw;
    }
    if (Object.keys(next).length === 0) {
      const { scope: _drop, ...rest } = params;
      void _drop;
      setParams(rest);
    } else {
      setParams({ ...params, scope: next });
    }
  }

  return (
    <div className="space-y-3">
      <div className="grid grid-cols-1 gap-3 md:grid-cols-2">
        <SelectField
          label="Metric"
          value={metric}
          onChange={(v) =>
            setParams({
              ...params,
              metric: v,
              // Drop scope keys that don't apply to the new metric
              scope: undefined,
            })
          }
          options={METRIC_KINDS.map((m) => ({ value: m.value, label: m.label }))}
        />
        <SelectField
          label="Comparator"
          value={comparator}
          onChange={(v) => setParams({ ...params, comparator: v })}
          options={[
            { value: ">", label: "> (greater than)" },
            { value: ">=", label: ">= (at least)" },
            { value: "<", label: "< (less than)" },
            { value: "<=", label: "<= (at most)" },
          ]}
        />
      </div>
      <div className="grid grid-cols-1 gap-3 md:grid-cols-3">
        <NumberField
          label="Value"
          hint="Threshold to compare against."
          value={value}
          step={0.01}
          onChange={(v) => setParams({ ...params, value: v === "" ? 0 : v })}
        />
        <NumberField
          label="Window (seconds)"
          hint="Lookback window. Default 120."
          value={windowSec === "" ? 120 : windowSec}
          min={1}
          onChange={(v) => setParams({ ...params, window_sec: v === "" ? 120 : v })}
        />
        <NumberField
          label="For (seconds)"
          hint="Sustained-for window. Default = window."
          value={forSec}
          min={1}
          placeholder="optional"
          onChange={(v) =>
            v === ""
              ? (() => {
                  const { for_sec: _drop, ...rest } = params;
                  void _drop;
                  setParams(rest);
                })()
              : setParams({ ...params, for_sec: v })
          }
        />
      </div>
      {scopeKeys.length > 0 && (
        <div className="rounded-md border border-border bg-panel-2/40 p-3">
          <p className="mb-2 text-[11px] font-semibold uppercase tracking-[0.08em] text-fg-subtle">
            Scope (optional)
          </p>
          <div className="grid grid-cols-1 gap-3 md:grid-cols-3">
            {scopeKeys.map((key) => (
              <Field
                key={key}
                label={key}
                hint={
                  key === "mountpoint"
                    ? "e.g. / or /var"
                    : key === "nic"
                      ? "e.g. eth0"
                      : key === "workload_id"
                        ? "workloads.id (uuid)"
                        : key === "monitor_id"
                          ? "monitors.id (uuid)"
                          : undefined
                }
              >
                <TextInput
                  value={asString(scope[key])}
                  placeholder={`Filter by ${key}`}
                  onChange={(e) => patchScope(key, e.target.value)}
                />
              </Field>
            ))}
          </div>
        </div>
      )}
    </div>
  );
}

function AgentOutdatedPane({ params, setParams }: PaneProps) {
  return (
    <div className="grid grid-cols-1 gap-3 md:grid-cols-2">
      <Field
        label="Minimum version"
        hint="Semver baseline. Leave empty to auto-compare against the freshest host's agent_version."
      >
        <TextInput
          value={asString(params.min_version)}
          placeholder="leave empty for auto"
          onChange={(e) => {
            const v = e.target.value.trim();
            if (v === "") {
              const { min_version: _drop, ...rest } = params;
              void _drop;
              setParams(rest);
            } else {
              setParams({ ...params, min_version: v });
            }
          }}
        />
      </Field>
    </div>
  );
}

function ImageUpdatePendingPane({ params, setParams }: PaneProps) {
  const hours = asNumberOrEmpty(params.min_age_hours);
  return (
    <div className="grid grid-cols-1 gap-3 md:grid-cols-2">
      <NumberField
        label="Minimum age (hours)"
        hint="update_available must persist this long before alerting. Default 24."
        value={hours === "" ? 24 : hours}
        min={0}
        onChange={(v) => setParams({ ...params, min_age_hours: v === "" ? 24 : v })}
      />
    </div>
  );
}

function PackageUpdateAvailablePane({ params, setParams }: PaneProps) {
  const thr = asNumberOrEmpty(params.threshold);
  return (
    <div className="grid grid-cols-1 gap-3 md:grid-cols-2">
      <NumberField
        label="Threshold"
        hint="Total updates_count > threshold fires. Default 50."
        value={thr === "" ? 50 : thr}
        min={0}
        onChange={(v) => setParams({ ...params, threshold: v === "" ? 50 : v })}
      />
    </div>
  );
}

function RepoMetadataStalePane({ params, setParams }: PaneProps) {
  const thr = asNumberOrEmpty(params.threshold_sec);
  return (
    <div className="grid grid-cols-1 gap-3 md:grid-cols-2">
      <NumberField
        label="Threshold (seconds)"
        hint="metadata_age_seconds > threshold_sec fires. Default 86400 (24h)."
        value={thr === "" ? 86400 : thr}
        min={0}
        onChange={(v) => setParams({ ...params, threshold_sec: v === "" ? 86400 : v })}
      />
    </div>
  );
}

function LoginAnomalyPane({ params, setParams }: PaneProps) {
  const kind = asString(params.kind, "new_source_ip");
  const win = asNumberOrEmpty(params.window_sec);
  const thr = asNumberOrEmpty(params.threshold);
  return (
    <div className="grid grid-cols-1 gap-3 md:grid-cols-3">
      <SelectField
        label="Kind"
        value={kind}
        onChange={(v) => setParams({ ...params, kind: v })}
        options={[
          { value: "new_source_ip", label: "New source IP" },
          { value: "root_success", label: "Root/SU success" },
          { value: "sudo_spike", label: "Sudo spike" },
        ]}
      />
      <NumberField
        label="Window (seconds)"
        hint="Default 86400 (24h)."
        value={win === "" ? 86400 : win}
        min={1}
        onChange={(v) => setParams({ ...params, window_sec: v === "" ? 86400 : v })}
      />
      <NumberField
        label="Threshold"
        hint="Events in window to fire. Default 1."
        value={thr === "" ? 1 : thr}
        min={1}
        onChange={(v) => setParams({ ...params, threshold: v === "" ? 1 : v })}
      />
    </div>
  );
}

function InventoryDriftPane({ params, setParams }: PaneProps) {
  const kind = asString(params.kind, "new_user");
  return (
    <div className="grid grid-cols-1 gap-3 md:grid-cols-2">
      <SelectField
        label="Kind"
        value={kind}
        onChange={(v) => setParams({ ...params, kind: v })}
        options={[
          { value: "new_user", label: "New user" },
          { value: "new_sudoer", label: "New sudoer" },
          { value: "new_disk", label: "New disk" },
          { value: "new_nic", label: "New NIC" },
          { value: "mac_changed", label: "MAC changed" },
          { value: "kernel_changed", label: "Kernel changed" },
          { value: "distro_changed", label: "Distro changed" },
          { value: "new_package", label: "New package" },
          { value: "removed_package", label: "Removed package" },
        ]}
      />
    </div>
  );
}

function FirewallStateChangePane({ params, setParams }: PaneProps) {
  const kind = asString(params.kind, "inactive");
  const dropThr = asNumberOrEmpty(params.drop_threshold);
  return (
    <div className="grid grid-cols-1 gap-3 md:grid-cols-2">
      <SelectField
        label="Kind"
        value={kind}
        onChange={(v) => {
          // drop_threshold only applies to rule_count_drop
          if (v !== "rule_count_drop") {
            const { drop_threshold: _drop, ...rest } = params;
            void _drop;
            setParams({ ...rest, kind: v });
          } else {
            setParams({ ...params, kind: v });
          }
        }}
        options={[
          { value: "inactive", label: "Inactive (firewall disabled)" },
          { value: "default_policy_weakened", label: "Default policy weakened" },
          { value: "rule_count_drop", label: "Rule count drop" },
        ]}
      />
      {kind === "rule_count_drop" && (
        <NumberField
          label="Drop threshold"
          hint="Rules removed since last baseline. Default 5."
          value={dropThr === "" ? 5 : dropThr}
          min={1}
          onChange={(v) => setParams({ ...params, drop_threshold: v === "" ? 5 : v })}
        />
      )}
    </div>
  );
}

function CrowdSecDecisionThresholdPane({ params, setParams }: PaneProps) {
  const thr = asNumberOrEmpty(params.threshold);
  return (
    <div className="grid grid-cols-1 gap-3 md:grid-cols-2">
      <NumberField
        label="Threshold"
        hint="Active decisions per host > threshold fires. Default 100."
        value={thr === "" ? 100 : thr}
        min={1}
        onChange={(v) => setParams({ ...params, threshold: v === "" ? 100 : v })}
      />
    </div>
  );
}

function NICLinkDownPane({ params, setParams }: PaneProps) {
  // Both flags default to true server-side; we surface them as toggles so the
  // user can opt back in to loopback/virtual NICs if they really want.
  const excludeLoopback = asBool(params.exclude_loopback, true);
  const excludeVirtual = asBool(params.exclude_virtual, true);
  return (
    <div className="grid grid-cols-1 gap-3 rounded-md border border-border bg-panel-2/40 p-3 md:grid-cols-2">
      <Toggle
        checked={excludeLoopback}
        onChange={(v) => setParams({ ...params, exclude_loopback: v })}
        label="Exclude loopback"
        hint="Default on. Ignores lo / loopback NICs."
      />
      <Toggle
        checked={excludeVirtual}
        onChange={(v) => setParams({ ...params, exclude_virtual: v })}
        label="Exclude virtual"
        hint="Default on. Ignores veth/docker0/bridges."
      />
    </div>
  );
}

function VMStateChangePane({ params, setParams }: PaneProps) {
  const subkind = asString(params.subkind, "any_transition");
  return (
    <div className="grid grid-cols-1 gap-3 md:grid-cols-2">
      <SelectField
        label="Subkind"
        value={subkind}
        onChange={(v) => setParams({ ...params, subkind: v })}
        options={[
          { value: "stopped", label: "Stopped" },
          { value: "autostart_violation", label: "Autostart violation" },
          { value: "any_transition", label: "Any transition (default)" },
        ]}
      />
    </div>
  );
}

function ContainerStateChangePane({ params, setParams }: PaneProps) {
  const statesArr = asStringArray(params.states);
  const states = statesArr.length > 0 ? statesArr : ["exited", "dead"];
  const exclude = asString(params.exclude_image_substring);
  const choices = ["created", "exited", "dead", "paused", "restarting"];

  function toggleState(s: string) {
    const next = states.includes(s) ? states.filter((x) => x !== s) : [...states, s];
    setParams({ ...params, states: next });
  }

  return (
    <div className="space-y-3">
      <Field label="States that fire" hint="Workload transitions to any of these states fire.">
        <div className="flex flex-wrap gap-2">
          {choices.map((s) => {
            const active = states.includes(s);
            return (
              <button
                key={s}
                type="button"
                onClick={() => toggleState(s)}
                className={`rounded-md px-2.5 py-1 text-xs font-medium ring-1 ring-inset transition-colors duration-150 ${
                  active
                    ? "bg-accent/15 text-accent ring-accent/40"
                    : "bg-panel ring-border text-fg-subtle hover:text-fg hover:bg-panel-2"
                }`}
              >
                {s}
              </button>
            );
          })}
        </div>
      </Field>
      <Field
        label="Exclude image substring"
        hint="Skip workloads whose image contains this substring (case-sensitive)."
      >
        <TextInput
          value={exclude}
          placeholder="e.g. pause / sleep-infinity"
          onChange={(e) => {
            const v = e.target.value;
            if (v === "") {
              const { exclude_image_substring: _drop, ...rest } = params;
              void _drop;
              setParams({ ...rest, states });
            } else {
              setParams({ ...params, states, exclude_image_substring: v });
            }
          }}
        />
      </Field>
    </div>
  );
}

function AuditActionPane({ params, setParams }: PaneProps) {
  const actionsArr = asStringArray(params.actions);
  // Local string state for the comma-separated input so the user can type
  // mid-token without losing focus or having trailing-comma stripped early.
  const [actionsRaw, setActionsRaw] = useState(actionsArr.join(", "));
  useEffect(() => {
    setActionsRaw(actionsArr.join(", "));
    // Re-sync only on external param mutations (JSON-mode round-trip).
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [JSON.stringify(actionsArr)]);

  function commitActions(raw: string) {
    const list = raw
      .split(",")
      .map((s) => s.trim())
      .filter(Boolean);
    if (list.length === 0) {
      setParams({ ...params, actions: [] });
    } else {
      setParams({ ...params, actions: list });
    }
  }

  return (
    <div className="space-y-3">
      <Field
        label="Actions"
        hint="Comma-separated action keys, e.g. admin.security.policy.update, host.delete"
      >
        <TextInput
          value={actionsRaw}
          placeholder="admin.security.policy.update, host.delete"
          onChange={(e) => setActionsRaw(e.target.value)}
          onBlur={(e) => commitActions(e.target.value)}
          className="font-mono"
        />
        {actionsArr.length > 0 && (
          <div className="mt-2 flex flex-wrap gap-1">
            {actionsArr.map((a) => (
              <span
                key={a}
                className="inline-flex items-center rounded-md bg-panel-2 px-1.5 py-0.5 font-mono text-[10px] text-accent ring-1 ring-inset ring-border"
              >
                {a}
              </span>
            ))}
          </div>
        )}
      </Field>
      <div className="grid grid-cols-1 gap-3 md:grid-cols-2">
        <Field label="Actor pattern (regex)" hint="Optional. Matches audit_log.actor.">
          <TextInput
            value={asString(params.actor_pattern)}
            placeholder="^admin@"
            onChange={(e) => {
              const v = e.target.value;
              if (v === "") {
                const { actor_pattern: _drop, ...rest } = params;
                void _drop;
                setParams(rest);
              } else {
                setParams({ ...params, actor_pattern: v });
              }
            }}
            className="font-mono"
          />
        </Field>
        <Field label="Target pattern (regex)" hint="Optional. Matches audit_log.target.">
          <TextInput
            value={asString(params.target_pattern)}
            placeholder="rule:.*"
            onChange={(e) => {
              const v = e.target.value;
              if (v === "") {
                const { target_pattern: _drop, ...rest } = params;
                void _drop;
                setParams(rest);
              } else {
                setParams({ ...params, target_pattern: v });
              }
            }}
            className="font-mono"
          />
        </Field>
      </div>
    </div>
  );
}

function HostFlapPane({ params, setParams }: PaneProps) {
  const win = asNumberOrEmpty(params.window_sec);
  const thr = asNumberOrEmpty(params.threshold);
  return (
    <div className="grid grid-cols-1 gap-3 md:grid-cols-2">
      <NumberField
        label="Window (seconds)"
        hint="Observation window. Default 1800 (30 min)."
        value={win === "" ? 1800 : win}
        min={60}
        onChange={(v) => setParams({ ...params, window_sec: v === "" ? 1800 : v })}
      />
      <NumberField
        label="Threshold"
        hint="Online/offline transitions in window. Default 6."
        value={thr === "" ? 6 : thr}
        min={2}
        onChange={(v) => setParams({ ...params, threshold: v === "" ? 6 : v })}
      />
    </div>
  );
}

// Dispatch table — keep in sync with CONDITION_TYPES above.
function ConditionParamsPane({
  conditionType,
  params,
  setParams,
}: {
  conditionType: NotificationConditionType;
  params: Params;
  setParams: (p: Params) => void;
}) {
  if (NO_PARAM_CONDITIONS.has(conditionType)) {
    return <NoParamsPane />;
  }
  switch (conditionType) {
    case "cert_expiring":
      return <CertExpiringPane params={params} setParams={setParams} />;
    case "login_failed_threshold":
      return <LoginFailedThresholdPane params={params} setParams={setParams} />;
    case "security_updates_pending":
      return <SecurityUpdatesPendingPane params={params} setParams={setParams} />;
    case "metric_threshold":
      return <MetricThresholdPane params={params} setParams={setParams} />;
    case "agent_outdated":
      return <AgentOutdatedPane params={params} setParams={setParams} />;
    case "image_update_pending":
      return <ImageUpdatePendingPane params={params} setParams={setParams} />;
    case "package_update_available":
      return <PackageUpdateAvailablePane params={params} setParams={setParams} />;
    case "repo_metadata_stale":
      return <RepoMetadataStalePane params={params} setParams={setParams} />;
    case "login_anomaly":
      return <LoginAnomalyPane params={params} setParams={setParams} />;
    case "inventory_drift":
      return <InventoryDriftPane params={params} setParams={setParams} />;
    case "firewall_state_change":
      return <FirewallStateChangePane params={params} setParams={setParams} />;
    case "crowdsec_decision_threshold":
      return <CrowdSecDecisionThresholdPane params={params} setParams={setParams} />;
    case "nic_link_down":
      return <NICLinkDownPane params={params} setParams={setParams} />;
    case "vm_state_change":
      return <VMStateChangePane params={params} setParams={setParams} />;
    case "container_state_change":
      return <ContainerStateChangePane params={params} setParams={setParams} />;
    case "audit_action":
      return <AuditActionPane params={params} setParams={setParams} />;
    case "host_flap":
      return <HostFlapPane params={params} setParams={setParams} />;
    default:
      return <NoParamsPane />;
  }
}

// ---------------------------------------------------------------------------
// Expert (JSON) pane
// ---------------------------------------------------------------------------

function ExpertJsonPane({
  params,
  setParams,
}: {
  params: Params;
  setParams: (p: Params) => void;
}) {
  // Local mirror of the JSON text. We only lift back into the params object
  // when the textarea parses cleanly (on every change). On blur we re-pretty
  // print so misformatted input snaps back to canonical shape.
  const [text, setText] = useState(() => JSON.stringify(params ?? {}, null, 2));
  const [err, setErr] = useState<string | null>(null);

  // Re-sync when params change externally (e.g. user toggled mode and the
  // typed pane mutated condition_params).
  useEffect(() => {
    setText(JSON.stringify(params ?? {}, null, 2));
    setErr(null);
  }, [JSON.stringify(params)]);

  function handleChange(next: string) {
    setText(next);
    if (next.trim() === "") {
      setErr(null);
      setParams({});
      return;
    }
    try {
      const parsed = JSON.parse(next);
      if (!parsed || typeof parsed !== "object" || Array.isArray(parsed)) {
        setErr("condition_params must be a JSON object");
        return;
      }
      setErr(null);
      setParams(parsed as Params);
    } catch (e) {
      setErr((e as Error).message);
    }
  }

  function handleBlur() {
    if (err) return;
    try {
      const parsed = text.trim() === "" ? {} : JSON.parse(text);
      setText(JSON.stringify(parsed, null, 2));
    } catch {
      // leave the user's text alone so they can fix it
    }
  }

  return (
    <div className="space-y-2">
      <textarea
        rows={10}
        value={text}
        onChange={(e) => handleChange(e.target.value)}
        onBlur={handleBlur}
        spellCheck={false}
        className="w-full rounded-md border border-border bg-panel px-3 py-2 font-mono text-xs text-fg placeholder:text-fg-subtle focus:border-accent focus:outline-none focus:ring-2 focus:ring-accent/30"
        placeholder='{\n  "threshold": 10\n}'
      />
      {err ? (
        <p className="text-xs text-fail">Invalid JSON: {err}</p>
      ) : (
        <p className="text-[11px] text-fg-subtle">
          Raw condition_params object. Toggle Expert off to drop back to the typed pane.
        </p>
      )}
    </div>
  );
}

// ---------------------------------------------------------------------------
// Main form
// ---------------------------------------------------------------------------

export function RuleForm({
  initial,
  channels,
  onCancel,
  onSaved,
}: {
  initial: NotificationRule | null;
  channels: NotificationChannel[];
  onCancel: () => void;
  onSaved: () => void;
}) {
  const tagsQuery = useQuery({
    queryKey: ["tags"],
    queryFn: () => api<{ tags: Array<{ tag: string; count: number }> }>("/v1/tags"),
  });
  const groupsQuery = useQuery({
    queryKey: ["groups"],
    queryFn: () => api<{ groups: HostGroup[] }>("/v1/groups"),
  });
  const hostsQuery = useQuery({
    queryKey: ["hosts"],
    queryFn: () => api<{ hosts: Host[] }>("/v1/hosts"),
  });

  const [name, setName] = useState(initial?.name ?? "");
  const [enabled, setEnabled] = useState(initial?.enabled ?? true);
  const [conditionType, setConditionType] = useState<NotificationConditionType>(
    initial?.condition_type ?? "host_offline",
  );
  const [severity, setSeverity] = useState<NotificationRule["severity"]>(initial?.severity ?? "warning");
  const [throttleSec, setThrottleSec] = useState(initial?.throttle_sec ?? 300);
  const [repeatIntervalSec, setRepeatIntervalSec] = useState(initial?.repeat_interval_sec ?? 0);
  const [notifyOnResolve, setNotifyOnResolve] = useState(initial?.notify_on_resolve ?? true);
  const [tagsRaw, setTagsRaw] = useState((initial?.target_tags ?? []).join(", "));
  const [groupIDs, setGroupIDs] = useState<string[]>(initial?.target_group_ids ?? []);
  const [hostIDs, setHostIDs] = useState<string[]>(initial?.target_host_ids ?? []);
  const [channelIDs, setChannelIDs] = useState<string[]>(initial?.channel_ids ?? []);
  const [error, setError] = useState<string | null>(null);

  // condition_params lives as an object; both the typed pane and the JSON
  // editor write to it through `setConditionParams`.
  const [conditionParams, setConditionParams] = useState<Params>(
    (initial?.condition_params as Params) ?? {},
  );

  // Expert mode toggle — exposes the raw JSON editor and hides the typed pane.
  const [expertMode, setExpertMode] = useState(false);

  const save = useMutation({
    mutationFn: () => {
      if (channelIDs.length === 0) {
        throw new Error("Pick at least one channel");
      }
      if (repeatIntervalSec !== 0 && (repeatIntervalSec < 60 || repeatIntervalSec > 86400)) {
        throw new Error("Repeat interval must be 0 or between 60 and 86400 seconds");
      }
      // Final sanity: roundtrip the params object through JSON so we fail
      // early on circular refs or unserialisable values (also matches what
      // the backend expects).
      let params: Params;
      try {
        params = JSON.parse(JSON.stringify(conditionParams ?? {})) as Params;
      } catch (e) {
        throw new Error(`condition_params is not serialisable JSON: ${(e as Error).message}`);
      }
      const tagList = tagsRaw
        .split(",")
        .map((s) => s.trim().toLowerCase())
        .filter(Boolean);
      const body: NotificationRuleInput = {
        name,
        enabled,
        condition_type: conditionType,
        condition_params: params,
        channel_ids: channelIDs,
        severity,
        throttle_sec: throttleSec,
        repeat_interval_sec: repeatIntervalSec,
        notify_on_resolve: notifyOnResolve,
        target_host_ids: hostIDs,
        target_tags: tagList,
        target_group_ids: groupIDs,
      };
      if (initial) {
        return api(`/v1/notifications/rules/${initial.id}`, {
          method: "PUT",
          body: JSON.stringify(body),
        });
      }
      return api("/v1/notifications/rules", {
        method: "POST",
        body: JSON.stringify(body),
      });
    },
    onSuccess: onSaved,
    onError: (err) => setError(err instanceof ApiError ? err.detail : (err as Error).message),
  });

  function submit(e: FormEvent) {
    e.preventDefault();
    setError(null);
    save.mutate();
  }

  const conditionMeta = CONDITION_TYPES.find((c) => c.value === conditionType)!;

  // Tag-chip rendering: parse the comma-separated free-text input into chips
  // for visual feedback. The raw text remains the source of truth so the user
  // can keep typing mid-tag without losing input focus.
  const tagChips = useMemo(
    () =>
      tagsRaw
        .split(",")
        .map((s) => s.trim().toLowerCase())
        .filter(Boolean),
    [tagsRaw],
  );
  function removeTag(tag: string) {
    const next = tagChips.filter((t) => t !== tag);
    setTagsRaw(next.join(", "));
  }

  const channelOptions = channels.map((c) => ({
    id: c.id,
    primary: c.name,
    secondary: c.type,
    icon: channelIcon(c.type),
  }));

  const hostOptions = (hostsQuery.data?.hosts ?? []).map((h) => ({
    id: h.id,
    primary: hostDisplay(h),
    secondary:
      (h.tags ?? []).length > 0 ? (
        <span className="inline-flex flex-wrap gap-1">
          {(h.tags ?? []).map((t) => (
            <span
              key={t}
              className="rounded-md bg-panel-2 px-1 font-mono text-[10px] text-accent"
            >
              #{t}
            </span>
          ))}
        </span>
      ) : (
        <span className="text-fg-subtle">no tags</span>
      ),
    icon: Server,
  }));

  const groupOptions = (groupsQuery.data?.groups ?? []).map((g) => ({
    id: g.id,
    primary: g.name,
    secondary: `${g.member_ids.length} member${g.member_ids.length === 1 ? "" : "s"}`,
    icon: Users,
  }));

  return (
    <Panel>
      <PanelHeader>
        <h3 className="text-sm font-semibold">{initial ? `Edit ${initial.name}` : "New rule"}</h3>
      </PanelHeader>
      <PanelBody>
        <form onSubmit={submit} className="space-y-5">
          {/* --- Identification --- */}
          <FormSection label="Identification">
            <div className="grid grid-cols-1 gap-3 md:grid-cols-[1fr_auto] md:items-end">
              <Field label="Name">
                <TextInput
                  required
                  value={name}
                  onChange={(e) => setName(e.target.value)}
                  placeholder="e.g. prod hosts offline"
                />
              </Field>
              <Field label="Severity">
                <PillGroup
                  value={severity}
                  onChange={(v) => setSeverity(v)}
                  label="Severity"
                  options={[
                    {
                      value: "info",
                      label: "info",
                      activeClass: "bg-ok/15 text-ok ring-ok/30",
                    },
                    {
                      value: "warning",
                      label: "warning",
                      activeClass: "bg-warn/15 text-warn ring-warn/30",
                    },
                    {
                      value: "critical",
                      label: "critical",
                      activeClass: "bg-fail/15 text-fail ring-fail/30",
                    },
                  ]}
                />
              </Field>
            </div>
          </FormSection>

          {/* --- Trigger --- */}
          <FormSection label="Trigger">
            <Field label="Condition" hint={conditionMeta.description}>
              <select
                value={conditionType}
                onChange={(e) => {
                  const next = e.target.value as NotificationConditionType;
                  setConditionType(next);
                  // Reset params when switching types so stale keys from the
                  // previous condition don't leak through. The expert mode
                  // user can paste them back if they really want.
                  if (next !== conditionType) {
                    setConditionParams({});
                  }
                }}
                className="w-full rounded-md border border-border bg-panel px-3 py-2 text-sm focus:border-accent focus:outline-none"
              >
                {CONDITION_TYPES.map((c) => (
                  <option key={c.value} value={c.value}>
                    {c.label}
                  </option>
                ))}
              </select>
            </Field>

            {/* Mode toggle */}
            <div className="flex items-center justify-between rounded-md border border-border bg-panel-2/40 px-3 py-2">
              <span className="inline-flex items-center gap-2 text-[11px] font-semibold uppercase tracking-[0.08em] text-fg-subtle">
                {expertMode ? (
                  <Code2 className="h-3.5 w-3.5" />
                ) : (
                  <Sliders className="h-3.5 w-3.5" />
                )}
                Condition parameters
              </span>
              <button
                type="button"
                role="switch"
                aria-checked={expertMode}
                onClick={() => setExpertMode((v) => !v)}
                className={`inline-flex items-center gap-2 rounded-md px-2 py-1 text-xs font-medium ring-1 ring-inset transition-colors duration-150 ${
                  expertMode
                    ? "bg-accent/15 text-accent ring-accent/40"
                    : "bg-panel ring-border text-fg-subtle hover:text-fg hover:bg-panel-2"
                }`}
                title="Toggle expert / raw JSON mode"
              >
                <Code2 className="h-3.5 w-3.5" />
                Expert (JSON)
              </button>
            </div>

            {expertMode ? (
              <ExpertJsonPane params={conditionParams} setParams={setConditionParams} />
            ) : (
              <ConditionParamsPane
                conditionType={conditionType}
                params={conditionParams}
                setParams={setConditionParams}
              />
            )}
          </FormSection>

          {/* --- Targets --- */}
          <FormSection
            label="Targets"
            hint="Empty = every host. Otherwise host must match any selection."
          >
            <Field label={`Hosts (${hostIDs.length} selected)`}>
              <CheckboxGrid
                options={hostOptions}
                selected={hostIDs}
                onToggle={(id) =>
                  setHostIDs((prev) =>
                    prev.includes(id) ? prev.filter((x) => x !== id) : [...prev, id],
                  )
                }
                empty="No hosts known yet."
                maxHeight="max-h-56"
              />
            </Field>

            <div className="grid grid-cols-1 gap-3 md:grid-cols-2">
              <Field
                label="Tags"
                hint={
                  tagsQuery.data?.tags?.length
                    ? `Existing: ${tagsQuery.data.tags.map((t) => t.tag).join(", ")}`
                    : "No tags defined yet."
                }
              >
                <TextInput
                  value={tagsRaw}
                  onChange={(e) => setTagsRaw(e.target.value)}
                  placeholder="prod, db"
                  className="font-mono"
                />
                {tagChips.length > 0 && (
                  <div className="mt-2 flex flex-wrap gap-1">
                    {tagChips.map((t) => (
                      <TagChip key={t} text={t} onRemove={() => removeTag(t)} />
                    ))}
                  </div>
                )}
              </Field>

              <Field label={`Groups (${groupIDs.length} selected)`}>
                <CheckboxGrid
                  options={groupOptions}
                  selected={groupIDs}
                  onToggle={(id) =>
                    setGroupIDs((prev) =>
                      prev.includes(id) ? prev.filter((x) => x !== id) : [...prev, id],
                    )
                  }
                  empty="No groups defined yet."
                  maxHeight="max-h-40"
                  columns="grid-cols-1"
                />
              </Field>
            </div>
          </FormSection>

          {/* --- Delivery --- */}
          <FormSection label="Delivery">
            <Field
              label={`Channels (${channelIDs.length} selected)`}
              hint="At least one channel must be selected."
            >
              <CheckboxGrid
                options={channelOptions}
                selected={channelIDs}
                onToggle={(id) =>
                  setChannelIDs((prev) =>
                    prev.includes(id) ? prev.filter((x) => x !== id) : [...prev, id],
                  )
                }
                empty="No channels available."
                maxHeight="max-h-56"
              />
            </Field>
          </FormSection>

          {/* --- Cadence --- */}
          <FormSection label="Cadence" divider={false}>
            <div className="grid grid-cols-1 gap-3 md:grid-cols-2">
              <Field label="Throttle (seconds)" hint="0 disables throttle.">
                <TextInput
                  type="number"
                  min={0}
                  value={throttleSec}
                  onChange={(e) => setThrottleSec(parseInt(e.target.value || "0", 10))}
                />
              </Field>
              <Field
                label="Repeat reminder (seconds)"
                hint="0 = fire once per outage. 60-86400 = re-send while still active."
              >
                <TextInput
                  type="number"
                  min={0}
                  max={86400}
                  value={repeatIntervalSec}
                  onChange={(e) => setRepeatIntervalSec(parseInt(e.target.value || "0", 10))}
                />
              </Field>
            </div>
            <div className="grid grid-cols-1 gap-3 rounded-md border border-border bg-panel-2/40 p-3 md:grid-cols-2">
              <Toggle
                checked={notifyOnResolve}
                onChange={setNotifyOnResolve}
                label="Notify on resolve"
                hint="Send an all-clear when the host or monitor recovers."
              />
              <Toggle
                checked={enabled}
                onChange={setEnabled}
                label="Enabled"
                hint="Disable to keep the rule but stop firing alerts."
              />
            </div>
          </FormSection>

          {error && <ErrorBox>{error}</ErrorBox>}

          <div className="flex items-center gap-2 pt-1">
            <Button variant="primary" type="submit" disabled={save.isPending}>
              {save.isPending ? "Saving…" : initial ? "Save" : "Create"}
            </Button>
            <Button type="button" onClick={onCancel}>
              Cancel
            </Button>
          </div>
        </form>
      </PanelBody>
    </Panel>
  );
}
