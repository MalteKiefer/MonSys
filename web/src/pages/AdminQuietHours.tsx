import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { Moon } from "lucide-react";
import { FormEvent, useEffect, useState } from "react";

import { Button, ErrorBox, Field, Panel, PanelBody, PanelHeader, SuccessBox, TextInput } from "../components/ui";
import { api, ApiError } from "../lib/api";
import type { NotificationSettings, NotificationSettingsInput } from "../lib/types";

// Operator-facing list of accepted timezone names. The server validates with
// time.LoadLocation, so anything in /usr/share/zoneinfo would also work; this
// is just a curated short list to keep the UI tidy.
const TZ_OPTIONS: { value: string; label: string }[] = [
  { value: "UTC", label: "UTC (server-local default)" },
  { value: "Europe/Berlin", label: "Europe/Berlin" },
  { value: "Europe/London", label: "Europe/London" },
  { value: "America/New_York", label: "America/New_York" },
  { value: "America/Los_Angeles", label: "America/Los_Angeles" },
];

const DAYS: { value: number; label: string; short: string }[] = [
  { value: 1, label: "Monday", short: "Mon" },
  { value: 2, label: "Tuesday", short: "Tue" },
  { value: 3, label: "Wednesday", short: "Wed" },
  { value: 4, label: "Thursday", short: "Thu" },
  { value: 5, label: "Friday", short: "Fri" },
  { value: 6, label: "Saturday", short: "Sat" },
  { value: 0, label: "Sunday", short: "Sun" },
];

export function AdminQuietHours() {
  const qc = useQueryClient();
  const settings = useQuery({
    queryKey: ["admin-quiet-hours"],
    queryFn: () => api<NotificationSettings>("/v1/admin/quiet-hours"),
  });

  return (
    <div className="mx-auto max-w-3xl space-y-6 p-6">
      <header>
        <h2 className="flex items-center gap-2 text-lg font-semibold">
          <Moon className="h-4 w-4 text-accent" /> Quiet hours
        </h2>
        <p className="text-sm text-fg-muted">
          When the configured window is active, the alert engine still records every alert in the
          history table (for audit) but skips dispatching to channels. This is independent of the
          per-agent quiet-hour setting — that one pauses telemetry pushes.
        </p>
      </header>

      {settings.isLoading ? (
        <p className="text-sm text-fg-muted">Loading…</p>
      ) : settings.error ? (
        <ErrorBox>{(settings.error as Error).message}</ErrorBox>
      ) : (
        <SettingsForm
          initial={settings.data!}
          onSaved={() => qc.invalidateQueries({ queryKey: ["admin-quiet-hours"] })}
        />
      )}
    </div>
  );
}

function SettingsForm({
  initial,
  onSaved,
}: {
  initial: NotificationSettings;
  onSaved: () => void;
}) {
  const [enabled, setEnabled] = useState(initial.quiet_enabled);
  const [start, setStart] = useState(initial.quiet_start);
  const [end, setEnd] = useState(initial.quiet_end);
  const [days, setDays] = useState<number[]>(initial.quiet_days || []);
  const [tz, setTz] = useState(initial.quiet_tz || "UTC");
  const [msg, setMsg] = useState<{ kind: "ok" | "err"; text: string } | null>(null);

  useEffect(() => {
    setEnabled(initial.quiet_enabled);
    setStart(initial.quiet_start);
    setEnd(initial.quiet_end);
    setDays(initial.quiet_days || []);
    setTz(initial.quiet_tz || "UTC");
  }, [initial]);

  const save = useMutation({
    mutationFn: () => {
      const body: NotificationSettingsInput = {
        quiet_enabled: enabled,
        quiet_start: start,
        quiet_end: end,
        quiet_days: [...days].sort((a, b) => a - b),
        quiet_tz: tz,
      };
      return api<NotificationSettings>("/v1/admin/quiet-hours", {
        method: "PUT",
        body: JSON.stringify(body),
      });
    },
    onSuccess: () => {
      setMsg({ kind: "ok", text: "Quiet hours saved." });
      onSaved();
    },
    onError: (err) =>
      setMsg({ kind: "err", text: err instanceof ApiError ? err.detail : "save failed" }),
  });

  function submit(e: FormEvent) {
    e.preventDefault();
    setMsg(null);
    save.mutate();
  }

  function toggleDay(d: number) {
    setDays((prev) => (prev.includes(d) ? prev.filter((x) => x !== d) : [...prev, d]));
  }

  // Tz dropdown should always include the currently saved value, even if it
  // was set to something outside our short curated list (e.g. via API).
  const tzOptions = TZ_OPTIONS.some((t) => t.value === tz)
    ? TZ_OPTIONS
    : [{ value: tz, label: tz }, ...TZ_OPTIONS];

  return (
    <Panel>
      <PanelHeader>
        <h3 className="text-sm font-semibold">Window</h3>
      </PanelHeader>
      <PanelBody>
        <form onSubmit={submit} className="space-y-5">
          <label className="flex items-center gap-2 text-sm">
            <input
              type="checkbox"
              checked={enabled}
              onChange={(e) => setEnabled(e.target.checked)}
            />
            <span>
              Enable quiet hours
              <span className="ml-2 text-xs text-fg-subtle">
                (alerts triggered inside the window are recorded but not delivered)
              </span>
            </span>
          </label>

          <div className="grid grid-cols-1 gap-3 md:grid-cols-2">
            <Field label="Start (HH:MM)">
              <TextInput
                type="time"
                required
                value={start}
                onChange={(e) => setStart(e.target.value)}
                disabled={!enabled}
              />
            </Field>
            <Field label="End (HH:MM)">
              <TextInput
                type="time"
                required
                value={end}
                onChange={(e) => setEnd(e.target.value)}
                disabled={!enabled}
              />
            </Field>
            <Field
              label="Timezone"
              hint="Window times are interpreted in this timezone. Unknown names fall back to UTC."
            >
              <select
                value={tz}
                onChange={(e) => setTz(e.target.value)}
                disabled={!enabled}
                className="w-full rounded-md border border-border bg-panel px-3 py-2 text-sm text-fg focus:border-accent focus:outline-none focus:ring-2 focus:ring-accent/30 disabled:opacity-50"
              >
                {tzOptions.map((o) => (
                  <option key={o.value} value={o.value}>
                    {o.label}
                  </option>
                ))}
              </select>
            </Field>
            <div />
          </div>

          <fieldset className="space-y-2 rounded-md border border-border bg-panel-2 p-3 text-sm">
            <legend className="px-1 text-xs uppercase tracking-wide text-fg-subtle">
              Active days
            </legend>
            <div className="flex flex-wrap gap-3">
              {DAYS.map((d) => (
                <label key={d.value} className="inline-flex items-center gap-1.5">
                  <input
                    type="checkbox"
                    checked={days.includes(d.value)}
                    onChange={() => toggleDay(d.value)}
                    disabled={!enabled}
                  />
                  <span>{d.short}</span>
                </label>
              ))}
            </div>
            <p className="text-xs text-fg-subtle">
              Empty = quiet hours never trigger. The default is every day.
            </p>
          </fieldset>

          {msg && msg.kind === "ok" && <SuccessBox>{msg.text}</SuccessBox>}
          {msg && msg.kind === "err" && <ErrorBox>{msg.text}</ErrorBox>}

          <div className="flex items-center gap-3">
            <Button type="submit" variant="primary" disabled={save.isPending}>
              {save.isPending ? "Saving…" : "Save quiet hours"}
            </Button>
            {initial.updated_at && (
              <span className="text-xs text-fg-subtle">
                Last updated {new Date(initial.updated_at).toLocaleString()}
                {initial.updated_by ? ` by ${initial.updated_by}` : ""}
              </span>
            )}
          </div>
        </form>
      </PanelBody>
    </Panel>
  );
}
