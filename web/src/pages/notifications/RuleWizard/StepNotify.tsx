// Step 3 — Notify. Name, channels, severity pill row, throttle + repeat
// (with a minute conversion hint), and the enabled toggle.

import { X } from "lucide-react";
import { useMemo, useState } from "react";

import { Field, TextInput } from "../../../components/ui";
import { PillGroup, Toggle } from "../../../components/notifications/FormParts";
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
      <Field label="Name" hint="Shown in the alerts list and notification subject.">
        <TextInput
          required
          value={draft.name}
          onChange={(e) => patch({ name: e.target.value })}
          placeholder="e.g. prod hosts offline"
        />
      </Field>

      <section>
        <p className="mb-2 text-[11px] font-semibold uppercase tracking-[0.08em] text-fg-subtle">
          Channels ({draft.channelIds.length} selected)
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
          empty="No channels available. Create one in Channels first."
          search={chSearch}
          onSearch={setChSearch}
          placeholder="Search channels…"
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
                    aria-label={`Remove channel ${c.name}`}
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

      <Field label="Severity">
        <PillGroup
          value={draft.severity}
          onChange={(v) => patch({ severity: v })}
          label="Severity"
          options={[
            { value: "info", label: "info", activeClass: "bg-ok/15 text-ok ring-ok/30" },
            { value: "warning", label: "warning", activeClass: "bg-warn/15 text-warn ring-warn/30" },
            { value: "critical", label: "critical", activeClass: "bg-fail/15 text-fail ring-fail/30" },
          ]}
        />
      </Field>

      <div className="grid grid-cols-1 gap-3 md:grid-cols-2">
        <Field
          label="Don't repeat for…"
          hint={
            draft.throttleSec > 0
              ? `≈ ${(draft.throttleSec / 60).toFixed(draft.throttleSec % 60 ? 1 : 0)} minute(s)`
              : "0 disables throttling — every fire delivers."
          }
        >
          <div className="flex items-center gap-2">
            <TextInput
              type="number"
              min={0}
              value={draft.throttleSec}
              onChange={(e) => patch({ throttleSec: parseInt(e.target.value || "0", 10) })}
            />
            <span className="text-xs text-fg-subtle">seconds</span>
          </div>
        </Field>
        <Field
          label="Repeat reminder"
          hint={
            draft.repeatIntervalSec === 0
              ? "0 = fire once per outage."
              : `Re-send every ${(draft.repeatIntervalSec / 60).toFixed(
                  draft.repeatIntervalSec % 60 ? 1 : 0,
                )} min while still active.`
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
            <span className="text-xs text-fg-subtle">seconds</span>
          </div>
        </Field>
      </div>

      <div className="grid grid-cols-1 gap-3 rounded-md border border-border bg-panel-2/40 p-3 md:grid-cols-2">
        <Toggle
          checked={draft.notifyOnResolve}
          onChange={(v) => patch({ notifyOnResolve: v })}
          label="Notify on resolve"
          hint="Send an all-clear when the host or monitor recovers."
        />
        <Toggle
          checked={draft.enabled}
          onChange={(v) => patch({ enabled: v })}
          label="Enabled"
          hint="Disable to keep the rule but stop firing alerts."
        />
      </div>
    </div>
  );
}
