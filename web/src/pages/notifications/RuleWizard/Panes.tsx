// Per-condition-type parameter panes. The dispatcher (ConditionParamsPane)
// at the bottom selects the right pane by NotificationConditionType. Each
// pane writes a Params object back through setParams; the JSON editor reads
// and writes the same shape so switching modes is lossless.

import { useEffect, useState } from "react";

import { Field, TextInput } from "../../../components/ui";
import { Toggle } from "../../../components/notifications/FormParts";
import type { NotificationConditionType } from "../../../lib/types";

import { METRIC_KINDS, NO_PARAM_CONDITIONS } from "./catalogue";
import {
  asBool,
  asNumberOrEmpty,
  asRecord,
  asString,
  asStringArray,
  type Params,
} from "./coerce";

// ---- Shared inputs --------------------------------------------------------

export function NumberField({
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

export function SelectField<T extends string>({
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

// ---- Per-type panes -------------------------------------------------------

type PaneProps = {
  params: Params;
  setParams: (next: Params) => void;
};

function NoParamsPane() {
  return (
    <p className="rounded-md border border-dashed border-border bg-panel-2/30 px-3 py-3 text-xs text-fg-subtle">
      No additional parameters — configure scope and delivery in the next steps.
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
              // Drop scope keys that don't apply to the new metric.
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

// Dispatch table — keep in sync with CONDITION_TYPES.
export function ConditionParamsPane({
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

// ---- Expert (raw JSON) pane ----------------------------------------------

export function ExpertJsonPane({
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
