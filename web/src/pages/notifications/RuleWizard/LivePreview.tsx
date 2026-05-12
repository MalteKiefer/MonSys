// Sticky live preview rendered to the right of the active wizard step. It
// composes a human-readable sentence from the current draft state and
// exposes a foldable raw JSON view so power users can sanity-check what
// the backend will receive.
//
// Multi-condition mode renders a bulleted "Alert when ANY of: …" list
// instead of the single-condition inline sentence.

import { ChevronDown, ChevronRight } from "lucide-react";
import { useMemo, useState, type ReactNode } from "react";

import { useT } from "../../../i18n/useT";
import { hostDisplay } from "../../../lib/utils";
import type { Host, HostGroup, NotificationChannel } from "../../../lib/types";

import { conditionSummary } from "./catalogue";
import type { RuleDraft } from "./draft";

type Translator = (key: string, opts?: Record<string, unknown>) => string;

function PreviewStrong({ children }: { children: ReactNode }) {
  return <strong className="font-semibold text-fg">{children}</strong>;
}

// scopePart renders "any host" / "hosts tagged #x" / etc. as a small piece of
// JSX. Shared between single- and multi-condition layouts.
function scopePart(
  draft: RuleDraft,
  hosts: Host[],
  groups: HostGroup[],
  t: Translator,
): ReactNode[] {
  const parts: ReactNode[] = [];
  if (
    draft.targetMode === "all" ||
    (draft.targetTags.length === 0 &&
      draft.targetGroupIds.length === 0 &&
      draft.targetHostIds.length === 0)
  ) {
    parts.push(<PreviewStrong key="any">{t("notifications:wizard.preview.scope_any_host")}</PreviewStrong>);
  } else if (draft.targetMode === "tags") {
    parts.push(t("notifications:wizard.preview.scope_tagged"));
    parts.push(
      <PreviewStrong key="tg">
        {draft.targetTags.map((tg) => `#${tg}`).join(", ")}
      </PreviewStrong>,
    );
  } else if (draft.targetMode === "groups") {
    const names = draft.targetGroupIds
      .map((id) => groups.find((g) => g.id === id)?.name ?? id.slice(0, 8))
      .join(", ");
    parts.push(t("notifications:wizard.preview.scope_in_group"));
    parts.push(<PreviewStrong key="g">{names}</PreviewStrong>);
  } else {
    const names = draft.targetHostIds
      .map((id) => {
        const h = hosts.find((x) => x.id === id);
        return h ? hostDisplay(h) : id.slice(0, 8);
      })
      .join(", ");
    parts.push(t("notifications:wizard.preview.scope_specific"));
    parts.push(<PreviewStrong key="h">{names}</PreviewStrong>);
  }
  return parts;
}

function deliveryPart(
  draft: RuleDraft,
  channels: NotificationChannel[],
  t: Translator,
): ReactNode[] {
  const parts: ReactNode[] = [];
  if (draft.channelIds.length > 0) {
    const names = draft.channelIds
      .map((id) => channels.find((c) => c.id === id)?.name ?? id.slice(0, 8))
      .join(", ");
    parts.push(t("notifications:wizard.preview.delivery_send_to"));
    parts.push(<PreviewStrong key="c">{names}</PreviewStrong>);
  } else {
    parts.push(t("notifications:wizard.preview.delivery_send_to"));
    parts.push(<PreviewStrong key="c">{t("notifications:wizard.preview.delivery_a_channel")}</PreviewStrong>);
  }
  parts.push(t("notifications:wizard.preview.delivery_with_severity"));
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
  const { t } = useT(["notifications", "common"]);
  const [showJson, setShowJson] = useState(false);
  const conds = draft.conditions;

  // Build the resolved batch-payload JSON the same way Save would, so power
  // users can verify the exact body the backend will receive.
  const resolved = useMemo(() => {
    try {
      const body = {
        name: draft.name,
        enabled: draft.enabled,
        severity: draft.severity,
        throttle_sec: draft.throttleSec,
        repeat_interval_sec: draft.repeatIntervalSec,
        notify_on_resolve: draft.notifyOnResolve,
        channel_ids: draft.channelIds,
        target_host_ids: draft.targetMode === "hosts" ? draft.targetHostIds : [],
        target_tags: draft.targetMode === "tags" ? draft.targetTags : [],
        target_group_ids: draft.targetMode === "groups" ? draft.targetGroupIds : [],
        conditions: conds.map((c) => ({
          condition_type: c.conditionType,
          condition_params: c.conditionParams,
        })),
      };
      return JSON.stringify(body, null, 2);
    } catch {
      return "{}";
    }
  }, [draft, conds]);

  return (
    <div className="sticky top-2 space-y-3 rounded-md border border-border bg-panel-2/40 p-3">
      <div>
        <p className="mb-1 text-[11px] font-semibold uppercase tracking-[0.08em] text-fg-subtle">
          {t("notifications:wizard.preview.title")}
        </p>
        {conds.length === 0 ? (
          <p className="text-sm leading-relaxed text-fg-subtle">
            {t("notifications:wizard.preview.add_to_see")}
          </p>
        ) : conds.length === 1 ? (
          <p className="text-sm leading-relaxed text-fg-muted">
            {t("notifications:wizard.preview.alert_when")}{" "}
            <PreviewStrong>
              {conds[0].conditionType
                ? conditionSummary(conds[0].conditionType, conds[0].conditionParams)
                : t("notifications:wizard.preview.condition_placeholder")}
            </PreviewStrong>{" "}
            on {scopePart(draft, hosts, groups, t).map((p, i) => (
              <span key={i}>{p}</span>
            ))}
            , {deliveryPart(draft, channels, t).map((p, i) => (
              <span key={i}>{p}</span>
            ))}
          </p>
        ) : (
          <div className="space-y-1.5 text-sm leading-relaxed text-fg-muted">
            <p>{t("notifications:wizard.preview.alert_when_any")}</p>
            <ul className="ml-3 list-disc space-y-0.5 marker:text-accent">
              {conds.map((c, i) => (
                <li key={i}>
                  <PreviewStrong>
                    {c.conditionType
                      ? conditionSummary(c.conditionType, c.conditionParams)
                      : t("notifications:wizard.preview.leg_no_type")}
                  </PreviewStrong>
                </li>
              ))}
            </ul>
            <p>
              on {scopePart(draft, hosts, groups, t).map((p, i) => (
                <span key={i}>{p}</span>
              ))}
              , {deliveryPart(draft, channels, t).map((p, i) => (
                <span key={i}>{p}</span>
              ))}
            </p>
          </div>
        )}
      </div>

      {draft.name && (
        <div className="rounded-md bg-panel p-2 ring-1 ring-inset ring-border">
          <p className="text-[10px] font-semibold uppercase tracking-[0.08em] text-fg-subtle">
            {t("notifications:wizard.preview.name_label")}
          </p>
          <p className="text-sm font-medium text-fg">{draft.name}</p>
          {conds.length > 1 && (
            <p className="mt-1 text-[10px] text-fg-subtle">
              {t("notifications:wizard.preview.rows_will_be_created_prefix", { count: conds.length })}
              <code className="font-mono">
                {draft.name || "…"}{t("notifications:wizard.preview.rows_will_be_created_suffix")}
              </code>
            </p>
          )}
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
          {t("notifications:wizard.preview.view_json")}
        </button>
        {showJson && (
          <pre className="mt-1 max-h-60 overflow-auto rounded-md bg-panel p-2 font-mono text-[10px] leading-snug text-fg ring-1 ring-inset ring-border">
            {resolved}
          </pre>
        )}
      </div>
    </div>
  );
}
