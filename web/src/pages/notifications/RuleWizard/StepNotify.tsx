// Step 3 — Notify. Name, channels, severity pill row, throttle + repeat
// (with a minute conversion hint), and the enabled toggle.

import { X } from "lucide-react";
import { useMemo, useState } from "react";

import { Field, TextInput } from "../../../components/ui";
import { PillGroup, Toggle } from "../../../components/notifications/FormParts";
import { useT } from "../../../i18n/useT";
import type { NotificationChannel } from "../../../lib/types";

import { channelIcon } from "./catalogue";
import type { RuleDraft } from "./draft";
import { MultiSelectList } from "./MultiSelectList";

export function StepNotify({
  draft,
  patch,
  channels,
}: {
  draft: RuleDraft;
  patch: (p: Partial<RuleDraft>) => void;
  channels: NotificationChannel[];
}) {
  const { t } = useT(["notifications", "common"]);
  const channelOptions = useMemo(
    () =>
      channels.map((c) => ({
        id: c.id,
        label: c.name,
        sub: c.type,
      })),
    [channels],
  );
  const [chSearch, setChSearch] = useState("");

  return (
    <div className="space-y-4">
      <Field
        label={t("notifications:wizard.notify.name_label")}
        hint={t("notifications:wizard.notify.name_hint")}
      >
        <TextInput
          required
          value={draft.name}
          onChange={(e) => patch({ name: e.target.value })}
          placeholder={t("notifications:wizard.notify.name_placeholder")}
        />
      </Field>

      <section>
        <p className="mb-2 text-[11px] font-semibold uppercase tracking-[0.08em] text-fg-subtle">
          {t("notifications:wizard.notify.channels_header", { count: draft.channelIds.length })}
        </p>
        <MultiSelectList
          items={channelOptions}
          selected={draft.channelIds}
          onToggle={(id) =>
            patch({
              channelIds: draft.channelIds.includes(id)
                ? draft.channelIds.filter((c) => c !== id)
                : [...draft.channelIds, id],
            })
          }
          empty={t("notifications:wizard.notify.channels_empty")}
          search={chSearch}
          onSearch={setChSearch}
          placeholder={t("notifications:wizard.notify.channels_search_placeholder")}
        />
        {draft.channelIds.length > 0 && (
          <div className="mt-2 flex flex-wrap gap-1">
            {draft.channelIds.map((id) => {
              const c = channels.find((x) => x.id === id);
              if (!c) return null;
              const Icon = channelIcon(c.type);
              return (
                <span
                  key={id}
                  className="inline-flex items-center gap-1 rounded-md bg-panel-2 pl-1.5 pr-1 py-0.5 text-[10px] text-fg-muted ring-1 ring-inset ring-border"
                >
                  <Icon className="h-3 w-3 text-accent" />
                  {c.name}
                  <button
                    type="button"
                    aria-label={t("notifications:wizard.notify.remove_channel_aria", { name: c.name })}
                    onClick={() =>
                      patch({ channelIds: draft.channelIds.filter((x) => x !== id) })
                    }
                    className="rounded p-0.5 text-fg-subtle hover:bg-fail/20 hover:text-fail"
                  >
                    <X className="h-3 w-3" />
                  </button>
                </span>
              );
            })}
          </div>
        )}
      </section>

      <Field label={t("notifications:wizard.notify.severity_label")}>
        <PillGroup
          value={draft.severity}
          onChange={(v) => patch({ severity: v })}
          label={t("notifications:wizard.notify.severity_label")}
          options={[
            { value: "info", label: t("notifications:wizard.notify.severity_info"), activeClass: "bg-ok/15 text-ok ring-ok/30" },
            { value: "warning", label: t("notifications:wizard.notify.severity_warning"), activeClass: "bg-warn/15 text-warn ring-warn/30" },
            { value: "critical", label: t("notifications:wizard.notify.severity_critical"), activeClass: "bg-fail/15 text-fail ring-fail/30" },
          ]}
        />
      </Field>

      <div className="grid grid-cols-1 gap-3 md:grid-cols-2">
        <Field
          label={t("notifications:wizard.notify.throttle_label")}
          hint={
            draft.throttleSec > 0
              ? t("notifications:wizard.notify.throttle_hint_minutes", {
                  value: (draft.throttleSec / 60).toFixed(draft.throttleSec % 60 ? 1 : 0),
                })
              : t("notifications:wizard.notify.throttle_hint_zero")
          }
        >
          <div className="flex items-center gap-2">
            <TextInput
              type="number"
              min={0}
              value={draft.throttleSec}
              onChange={(e) => patch({ throttleSec: parseInt(e.target.value || "0", 10) })}
            />
            <span className="text-xs text-fg-subtle">{t("notifications:wizard.notify.throttle_unit")}</span>
          </div>
        </Field>
        <Field
          label={t("notifications:wizard.notify.repeat_label")}
          hint={
            draft.repeatIntervalSec === 0
              ? t("notifications:wizard.notify.repeat_hint_zero")
              : t("notifications:wizard.notify.repeat_hint_active", {
                  value: (draft.repeatIntervalSec / 60).toFixed(draft.repeatIntervalSec % 60 ? 1 : 0),
                })
          }
        >
          <div className="flex items-center gap-2">
            <TextInput
              type="number"
              min={0}
              max={86400}
              value={draft.repeatIntervalSec}
              onChange={(e) => patch({ repeatIntervalSec: parseInt(e.target.value || "0", 10) })}
            />
            <span className="text-xs text-fg-subtle">{t("notifications:wizard.notify.repeat_unit")}</span>
          </div>
        </Field>
      </div>

      <div className="grid grid-cols-1 gap-3 rounded-md border border-border bg-panel-2/40 p-3 md:grid-cols-2">
        <Toggle
          checked={draft.notifyOnResolve}
          onChange={(v) => patch({ notifyOnResolve: v })}
          label={t("notifications:wizard.notify.notify_on_resolve_label")}
          hint={t("notifications:wizard.notify.notify_on_resolve_hint")}
        />
        <Toggle
          checked={draft.enabled}
          onChange={(v) => patch({ enabled: v })}
          label={t("notifications:wizard.notify.enabled_label")}
          hint={t("notifications:wizard.notify.enabled_hint")}
        />
      </div>
    </div>
  );
}
