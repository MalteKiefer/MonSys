// Sticky live preview rendered to the right of the active wizard step. It
// composes a human-readable sentence from the current draft state and
// exposes a foldable raw JSON view so power users can sanity-check what
// the backend will receive.

import { ChevronDown, ChevronRight } from "lucide-react";
import { useMemo, useState, type ReactNode } from "react";

import { hostDisplay } from "../../../lib/utils";
import type { Host, HostGroup, NotificationChannel } from "../../../lib/types";

import { METRIC_KINDS } from "./catalogue";
import { asNumberOrEmpty, asString, asStringArray } from "./coerce";
import type { RuleDraft } from "./draft";

function PreviewStrong({ children }: { children: ReactNode }) {
  return <strong className="font-semibold text-fg">{children}</strong>;
}

// previewParts builds an array of inline parts so the React-rendered span
// can interleave plain text and <strong> highlights without dangerouslySet…
function previewParts(
  draft: RuleDraft,
  channels: NotificationChannel[],
  hosts: Host[],
  groups: HostGroup[],
): ReactNode[] {
  const parts: ReactNode[] = [];
  const ct = draft.conditionType;

  parts.push("Alert when ");

  if (!ct) {
    parts.push("a condition is picked");
  } else {
    switch (ct) {
      case "metric_threshold": {
        const metric = asString(draft.conditionParams.metric, "cpu_usage_pct");
        const comparator = asString(draft.conditionParams.comparator, ">");
        const value = asNumberOrEmpty(draft.conditionParams.value);
        const win = asNumberOrEmpty(draft.conditionParams.window_sec);
        const metricLabel = METRIC_KINDS.find((m) => m.value === metric)?.label ?? metric;
        parts.push(<PreviewStrong key="m">{metricLabel}</PreviewStrong>);
        parts.push(` ${comparator} `);
        parts.push(<PreviewStrong key="v">{value === "" ? "?" : String(value)}</PreviewStrong>);
        if (win !== "") {
          const minutes = Math.round((win as number) / 60);
          parts.push(" for ");
          parts.push(
            <PreviewStrong key="w">{minutes >= 1 ? `${minutes} min` : `${win}s`}</PreviewStrong>,
          );
        }
        break;
      }
      case "host_offline":
        parts.push("a host goes ");
        parts.push(<PreviewStrong key="o">offline</PreviewStrong>);
        parts.push(" for the configured liveness window");
        break;
      case "host_flap": {
        const win = asNumberOrEmpty(draft.conditionParams.window_sec);
        const thr = asNumberOrEmpty(draft.conditionParams.threshold);
        parts.push("a host flaps more than ");
        parts.push(<PreviewStrong key="t">{thr === "" ? "6" : String(thr)}</PreviewStrong>);
        parts.push(" times within ");
        const winSec = win === "" ? 1800 : (win as number);
        const minutes = Math.round(winSec / 60);
        parts.push(<PreviewStrong key="w">{`${minutes} min`}</PreviewStrong>);
        break;
      }
      case "unexpected_reboot":
        parts.push("a host reboots ");
        parts.push(<PreviewStrong key="u">unexpectedly</PreviewStrong>);
        break;
      case "monitor_failed":
        parts.push("a ");
        parts.push(<PreviewStrong key="mf">monitor</PreviewStrong>);
        parts.push(" reports a non-OK status");
        break;
      case "cert_expiring": {
        const days = asNumberOrEmpty(draft.conditionParams.days_threshold);
        parts.push("a certificate expires within ");
        parts.push(<PreviewStrong key="d">{days === "" ? "30" : String(days)}</PreviewStrong>);
        parts.push(" days");
        break;
      }
      case "login_failed_threshold": {
        const thr = asNumberOrEmpty(draft.conditionParams.threshold);
        const win = asNumberOrEmpty(draft.conditionParams.window_sec);
        parts.push("more than ");
        parts.push(<PreviewStrong key="t">{thr === "" ? "10" : String(thr)}</PreviewStrong>);
        parts.push(" failed logins occur in ");
        parts.push(<PreviewStrong key="w">{`${win === "" ? 300 : win}s`}</PreviewStrong>);
        break;
      }
      case "security_updates_pending": {
        const thr = asNumberOrEmpty(draft.conditionParams.threshold);
        parts.push("a host has ≥ ");
        parts.push(<PreviewStrong key="t">{thr === "" ? "1" : String(thr)}</PreviewStrong>);
        parts.push(" pending security updates");
        break;
      }
      case "agent_outdated": {
        const min = asString(draft.conditionParams.min_version);
        parts.push("the agent version is below ");
        parts.push(<PreviewStrong key="v">{min || "the latest seen"}</PreviewStrong>);
        break;
      }
      case "image_update_pending": {
        const h = asNumberOrEmpty(draft.conditionParams.min_age_hours);
        parts.push("a container has a pending image update older than ");
        parts.push(<PreviewStrong key="h">{`${h === "" ? 24 : h}h`}</PreviewStrong>);
        break;
      }
      case "package_update_available": {
        const thr = asNumberOrEmpty(draft.conditionParams.threshold);
        parts.push("a host has more than ");
        parts.push(<PreviewStrong key="t">{thr === "" ? "50" : String(thr)}</PreviewStrong>);
        parts.push(" pending package updates");
        break;
      }
      case "pending_reboot":
        parts.push("a host has a ");
        parts.push(<PreviewStrong key="r">pending reboot</PreviewStrong>);
        break;
      case "repo_metadata_stale": {
        const s = asNumberOrEmpty(draft.conditionParams.threshold_sec);
        const secs = s === "" ? 86400 : (s as number);
        const hours = Math.round(secs / 3600);
        parts.push("repository metadata is older than ");
        parts.push(<PreviewStrong key="r">{`${hours}h`}</PreviewStrong>);
        break;
      }
      case "login_anomaly": {
        const k = asString(draft.conditionParams.kind, "new_source_ip");
        parts.push("a login anomaly of kind ");
        parts.push(<PreviewStrong key="k">{k}</PreviewStrong>);
        parts.push(" is detected");
        break;
      }
      case "inventory_drift": {
        const k = asString(draft.conditionParams.kind, "new_user");
        parts.push("inventory drift of kind ");
        parts.push(<PreviewStrong key="k">{k}</PreviewStrong>);
        parts.push(" is observed");
        break;
      }
      case "firewall_state_change": {
        const k = asString(draft.conditionParams.kind, "inactive");
        parts.push("the firewall changes state: ");
        parts.push(<PreviewStrong key="k">{k}</PreviewStrong>);
        break;
      }
      case "fail2ban_jail_disappeared":
        parts.push("a previously-known ");
        parts.push(<PreviewStrong key="f">fail2ban jail</PreviewStrong>);
        parts.push(" disappears");
        break;
      case "crowdsec_decision_threshold": {
        const thr = asNumberOrEmpty(draft.conditionParams.threshold);
        parts.push("CrowdSec active decisions exceed ");
        parts.push(<PreviewStrong key="t">{thr === "" ? "100" : String(thr)}</PreviewStrong>);
        break;
      }
      case "nic_link_down":
        parts.push("a ");
        parts.push(<PreviewStrong key="n">NIC link</PreviewStrong>);
        parts.push(" goes down");
        break;
      case "nic_bond_degraded":
        parts.push("a ");
        parts.push(<PreviewStrong key="b">NIC bond</PreviewStrong>);
        parts.push(" is degraded");
        break;
      case "vm_state_change": {
        const sub = asString(draft.conditionParams.subkind, "any_transition");
        parts.push("a VM transitions (");
        parts.push(<PreviewStrong key="s">{sub}</PreviewStrong>);
        parts.push(")");
        break;
      }
      case "container_state_change": {
        const states = asStringArray(draft.conditionParams.states);
        const shown = states.length > 0 ? states.join(", ") : "exited, dead";
        parts.push("a container enters one of: ");
        parts.push(<PreviewStrong key="s">{shown}</PreviewStrong>);
        break;
      }
      case "audit_action": {
        const actions = asStringArray(draft.conditionParams.actions);
        parts.push("an audit action matches: ");
        parts.push(
          <PreviewStrong key="a">{actions.length > 0 ? actions.join(", ") : "—"}</PreviewStrong>,
        );
        break;
      }
      default:
        parts.push(<PreviewStrong key="x">{ct}</PreviewStrong>);
    }
  }

  // Scope
  parts.push(" on ");
  if (
    draft.targetMode === "all" ||
    (draft.targetTags.length === 0 &&
      draft.targetGroupIds.length === 0 &&
      draft.targetHostIds.length === 0)
  ) {
    parts.push(<PreviewStrong key="any">any host</PreviewStrong>);
  } else if (draft.targetMode === "tags") {
    parts.push("hosts tagged ");
    parts.push(
      <PreviewStrong key="tg">{draft.targetTags.map((t) => `#${t}`).join(", ")}</PreviewStrong>,
    );
  } else if (draft.targetMode === "groups") {
    const names = draft.targetGroupIds
      .map((id) => groups.find((g) => g.id === id)?.name ?? id.slice(0, 8))
      .join(", ");
    parts.push("hosts in group ");
    parts.push(<PreviewStrong key="g">{names}</PreviewStrong>);
  } else {
    const names = draft.targetHostIds
      .map((id) => {
        const h = hosts.find((x) => x.id === id);
        return h ? hostDisplay(h) : id.slice(0, 8);
      })
      .join(", ");
    parts.push("hosts ");
    parts.push(<PreviewStrong key="h">{names}</PreviewStrong>);
  }

  // Delivery
  if (draft.channelIds.length > 0) {
    const names = draft.channelIds
      .map((id) => channels.find((c) => c.id === id)?.name ?? id.slice(0, 8))
      .join(", ");
    parts.push(", send to ");
    parts.push(<PreviewStrong key="c">{names}</PreviewStrong>);
  } else {
    parts.push(", send to ");
    parts.push(<PreviewStrong key="c">a channel</PreviewStrong>);
  }

  parts.push(" with severity ");
  parts.push(<PreviewStrong key="sev">{draft.severity}</PreviewStrong>);
  parts.push(".");

  return parts;
}

export function LivePreview({
  draft,
  channels,
  hosts,
  groups,
}: {
  draft: RuleDraft;
  channels: NotificationChannel[];
  hosts: Host[];
  groups: HostGroup[];
}) {
  const [showJson, setShowJson] = useState(false);
  const sentence = previewParts(draft, channels, hosts, groups);

  // Build the resolved condition_params JSON the same way Save would.
  const resolved = useMemo(() => {
    try {
      return JSON.stringify(draft.conditionParams ?? {}, null, 2);
    } catch {
      return "{}";
    }
  }, [draft.conditionParams]);

  return (
    <div className="sticky top-2 space-y-3 rounded-md border border-border bg-panel-2/40 p-3">
      <div>
        <p className="mb-1 text-[11px] font-semibold uppercase tracking-[0.08em] text-fg-subtle">
          Preview
        </p>
        <p className="text-sm leading-relaxed text-fg-muted">
          {sentence.map((p, i) => (
            <span key={i}>{p}</span>
          ))}
        </p>
      </div>

      {draft.name && (
        <div className="rounded-md bg-panel p-2 ring-1 ring-inset ring-border">
          <p className="text-[10px] font-semibold uppercase tracking-[0.08em] text-fg-subtle">
            Name
          </p>
          <p className="text-sm font-medium text-fg">{draft.name}</p>
        </div>
      )}

      <div>
        <button
          type="button"
          onClick={() => setShowJson((v) => !v)}
          className="inline-flex items-center gap-1 text-[11px] font-medium text-fg-muted hover:text-fg"
        >
          {showJson ? (
            <ChevronDown className="h-3 w-3" />
          ) : (
            <ChevronRight className="h-3 w-3" />
          )}
          View JSON
        </button>
        {showJson && (
          <pre className="mt-1 max-h-48 overflow-auto rounded-md bg-panel p-2 font-mono text-[10px] leading-snug text-fg ring-1 ring-inset ring-border">
            {resolved}
          </pre>
        )}
      </div>
    </div>
  );
}
