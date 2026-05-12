import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { Activity, Clock, Moon, PlayCircle } from "lucide-react";
import { FormEvent, useEffect, useMemo, useState } from "react";

import { Page } from "../components/page";
import {
  Button,
  ErrorBox,
  Field,
  Panel,
  PanelBody,
  PanelHeader,
  Skeleton,
  StatusPill,
  SuccessBox,
  TabItem,
  Tabs,
  TextInput,
} from "../components/ui";
import { useT } from "../i18n/useT";
import { api, ApiError } from "../lib/api";
import type { NotificationSettings, NotificationSettingsInput } from "../lib/types";

// Operator-facing list of accepted timezone names. The server validates with
// time.LoadLocation, so anything in /usr/share/zoneinfo would also work; this
// is just a curated short list to keep the UI tidy.
function useTzOptions() {
  const { t } = useT(["admin"]);
  return useMemo(
    () => [
      { value: "UTC", label: t("admin:quietHours.tz.utc") },
      { value: "Europe/Berlin", label: "Europe/Berlin" },
      { value: "Europe/London", label: "Europe/London" },
      { value: "America/New_York", label: "America/New_York" },
      { value: "America/Los_Angeles", label: "America/Los_Angeles" },
    ],
    [t],
  );
}

function useDays() {
  const { t } = useT(["admin"]);
  return useMemo(
    () => [
      { value: 1, label: "Monday", short: t("admin:quietHours.days.mon") },
      { value: 2, label: "Tuesday", short: t("admin:quietHours.days.tue") },
      { value: 3, label: "Wednesday", short: t("admin:quietHours.days.wed") },
      { value: 4, label: "Thursday", short: t("admin:quietHours.days.thu") },
      { value: 5, label: "Friday", short: t("admin:quietHours.days.fri") },
      { value: 6, label: "Saturday", short: t("admin:quietHours.days.sat") },
      { value: 0, label: "Sunday", short: t("admin:quietHours.days.sun") },
    ],
    [t],
  );
}

// The page exposes a single global quiet-hours singleton (the server has no
// per-channel override and no silenced-alert history endpoint), so the tabs
// only split presentation, not data fetch boundaries. "schedule" hosts the
// edit form; "timeline" renders the same live snapshot as a weekly grid plus
// the runtime evaluator ("Test now").
type TabKey = "schedule" | "timeline";

export function AdminQuietHours() {
  const { t } = useT(["admin", "common"]);
  const qc = useQueryClient();
  const settings = useQuery({
    queryKey: ["admin-quiet-hours"],
    queryFn: () => api<NotificationSettings>("/v1/admin/quiet-hours"),
  });

  return (
    <Page
      title={
        <span className="flex items-center gap-2">
          <Moon className="h-5 w-5 text-accent" /> {t("admin:quietHours.title")}
        </span>
      }
      subtitle={t("admin:quietHours.subtitle")}
    >
      {settings.isLoading ? (
        <Skeleton className="h-64" />
      ) : settings.error ? (
        <ErrorBox>{(settings.error as Error).message}</ErrorBox>
      ) : (
        <SettingsForm
          initial={settings.data!}
          onSaved={() => qc.invalidateQueries({ queryKey: ["admin-quiet-hours"] })}
        />
      )}
    </Page>
  );
}

function SettingsForm({
  initial,
  onSaved,
}: {
  initial: NotificationSettings;
  onSaved: () => void;
}) {
  const { t } = useT(["admin", "common"]);
  const TZ_OPTIONS = useTzOptions();
  const DAYS = useDays();

  const tabItems: ReadonlyArray<TabItem<TabKey>> = useMemo(
    () => [
      { key: "schedule", label: t("admin:quietHours.tabs.schedule"), icon: Clock },
      { key: "timeline", label: t("admin:quietHours.tabs.timeline"), icon: Activity },
    ],
    [t],
  );

  const [tab, setTab] = useState<TabKey>("schedule");
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
      setMsg({ kind: "ok", text: t("admin:quietHours.window.saved") });
      onSaved();
    },
    onError: (err) =>
      setMsg({
        kind: "err",
        text: err instanceof ApiError ? err.detail : t("admin:quietHours.window.saveFailed"),
      }),
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
  const tzOptions = TZ_OPTIONS.some((tt) => tt.value === tz)
    ? TZ_OPTIONS
    : [{ value: tz, label: tz }, ...TZ_OPTIONS];

  return (
    <div className="space-y-4">
      <Tabs
        items={tabItems}
        value={tab}
        onChange={setTab}
        idPrefix="qh-tab"
        panelIdPrefix="qh-panel"
      />

      {tab === "schedule" && (
        <div
          role="tabpanel"
          id="qh-panel-schedule"
          aria-labelledby="qh-tab-schedule"
          className="space-y-5"
        >
          <Panel>
            <PanelHeader>
              <h3 className="text-sm font-semibold">{t("admin:quietHours.window.title")}</h3>
              {enabled ? (
                <StatusPill status="info">{t("admin:quietHours.status.enabled")}</StatusPill>
              ) : (
                <StatusPill status="offline">{t("admin:quietHours.status.disabled")}</StatusPill>
              )}
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
                    {t("admin:quietHours.window.enable")}
                    <span className="ml-2 text-xs text-fg-subtle">
                      {t("admin:quietHours.window.enableHint")}
                    </span>
                  </span>
                </label>

                <div className="grid grid-cols-1 gap-3 md:grid-cols-2">
                  <Field label={t("admin:quietHours.window.start")}>
                    <TextInput
                      type="time"
                      required
                      value={start}
                      onChange={(e) => setStart(e.target.value)}
                      disabled={!enabled}
                    />
                  </Field>
                  <Field label={t("admin:quietHours.window.end")}>
                    <TextInput
                      type="time"
                      required
                      value={end}
                      onChange={(e) => setEnd(e.target.value)}
                      disabled={!enabled}
                    />
                  </Field>
                  <Field
                    label={t("admin:quietHours.window.timezone")}
                    hint={t("admin:quietHours.window.timezoneHint")}
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
                    {t("admin:quietHours.window.activeDays")}
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
                    {t("admin:quietHours.window.daysHint")}
                  </p>
                </fieldset>

                {msg && msg.kind === "ok" && <SuccessBox>{msg.text}</SuccessBox>}
                {msg && msg.kind === "err" && <ErrorBox>{msg.text}</ErrorBox>}

                <div className="flex items-center gap-3">
                  <Button type="submit" variant="primary" disabled={save.isPending}>
                    {save.isPending
                      ? t("common:actions.saving")
                      : t("admin:quietHours.window.save")}
                  </Button>
                  {initial.updated_at && (
                    <span className="text-xs text-fg-subtle">
                      {t("admin:quietHours.window.lastUpdated", {
                        when: new Date(initial.updated_at).toLocaleString(),
                      })}
                      {initial.updated_by
                        ? t("admin:quietHours.window.lastUpdatedBy", { user: initial.updated_by })
                        : ""}
                    </span>
                  )}
                </div>
              </form>
            </PanelBody>
          </Panel>
        </div>
      )}

      {tab === "timeline" && (
        <div
          role="tabpanel"
          id="qh-panel-timeline"
          aria-labelledby="qh-tab-timeline"
          className="space-y-5"
        >
          <TimelinePanel enabled={enabled} start={start} end={end} days={days} tz={tz} />
        </div>
      )}
    </div>
  );
}

// ---- Timeline visualization ----------------------------------------------

// Parse a "HH:MM" string into a fractional hour value in [0, 24]. Returns NaN
// if the input is malformed; callers fall back gracefully.
function parseHHMM(v: string): number {
  if (!v) return NaN;
  const m = /^([0-2]?\d):([0-5]\d)$/.exec(v.trim());
  if (!m) return NaN;
  const h = Number(m[1]);
  const mm = Number(m[2]);
  if (h < 0 || h > 24 || mm < 0 || mm >= 60) return NaN;
  return h + mm / 60;
}

// Get the wall-clock weekday and hour-of-day in a target IANA timezone.
// Falls back to local time if Intl can't resolve the zone.
function nowInZone(tz: string, now: Date): { day: number; hour: number } {
  try {
    const fmt = new Intl.DateTimeFormat("en-US", {
      timeZone: tz,
      hour12: false,
      weekday: "short",
      hour: "2-digit",
      minute: "2-digit",
    });
    const parts = fmt.formatToParts(now);
    const wd = parts.find((p) => p.type === "weekday")?.value ?? "";
    let h = Number(parts.find((p) => p.type === "hour")?.value ?? "0");
    const m = Number(parts.find((p) => p.type === "minute")?.value ?? "0");
    if (h === 24) h = 0;
    const map: Record<string, number> = {
      Sun: 0,
      Mon: 1,
      Tue: 2,
      Wed: 3,
      Thu: 4,
      Fri: 5,
      Sat: 6,
    };
    return { day: map[wd] ?? now.getDay(), hour: h + m / 60 };
  } catch {
    return { day: now.getDay(), hour: now.getHours() + now.getMinutes() / 60 };
  }
}

// For a given day, return the [start, end) hour ranges that the quiet window
// covers on that day. A wrap-around window (start>end, e.g. 22:00–06:00)
// renders as two fragments on the same row — [start, 24) and [0, end). This
// matches the runtime "is now within the window today?" check.
function rangesForDay(
  enabled: boolean,
  days: number[],
  startH: number,
  endH: number,
  day: number,
): { from: number; to: number }[] {
  if (!enabled) return [];
  const isActive = days.length === 0 || days.includes(day);
  if (!isActive) return [];
  if (!Number.isFinite(startH) || !Number.isFinite(endH)) return [];
  if (startH === endH) return [];
  if (startH < endH) return [{ from: startH, to: endH }];
  const out: { from: number; to: number }[] = [];
  if (startH < 24) out.push({ from: startH, to: 24 });
  if (endH > 0) out.push({ from: 0, to: endH });
  return out;
}

// True if the engine would currently mute alerts given the inputs and the
// supplied "now" instant. Mirrors the runtime check.
function isQuietNow(
  enabled: boolean,
  days: number[],
  startH: number,
  endH: number,
  tz: string,
  now: Date,
): boolean {
  if (!enabled) return false;
  if (!Number.isFinite(startH) || !Number.isFinite(endH)) return false;
  const { day, hour } = nowInZone(tz, now);
  const dayActive = days.length === 0 || days.includes(day);
  if (!dayActive) return false;
  if (startH === endH) return false;
  if (startH < endH) return hour >= startH && hour < endH;
  return hour >= startH || hour < endH;
}

function TimelinePanel({
  enabled,
  start,
  end,
  days,
  tz,
}: {
  enabled: boolean;
  start: string;
  end: string;
  days: number[];
  tz: string;
}) {
  const { t } = useT(["admin"]);
  const DAYS = useDays();
  const startH = parseHHMM(start);
  const endH = parseHHMM(end);

  // Render in the same Mon..Sun order as the day picker so the eye can
  // correlate them at a glance.
  const rows = useMemo(
    () =>
      DAYS.map((d) => ({
        ...d,
        ranges: rangesForDay(enabled, days, startH, endH, d.value),
      })),
    [enabled, days, startH, endH, DAYS],
  );

  // "Test now" preview state. The snapshot is stable until the user clicks
  // again or edits an input — editing wipes the preview so it never lies.
  const [previewAt, setPreviewAt] = useState<Date | null>(null);
  useEffect(() => {
    setPreviewAt(null);
  }, [enabled, start, end, days, tz]);

  const previewQuiet = previewAt
    ? isQuietNow(enabled, days, startH, endH, tz, previewAt)
    : null;

  // Live cursor — refresh every minute so the "now" indicator drifts smoothly
  // without a per-second redraw.
  const [tick, setTick] = useState(0);
  useEffect(() => {
    const id = setInterval(() => setTick((n) => n + 1), 60_000);
    return () => clearInterval(id);
  }, []);
  const liveNow = useMemo(() => new Date(), [tick]);
  const live = nowInZone(tz, liveNow);

  // SVG geometry — one row per day, 24 hourly columns. Reserve a label gutter
  // on the left and divide the rest evenly. The viewBox is unitless so the
  // figure scales fluidly with the parent width.
  const ROW_H = 22;
  const HEAD_H = 18;
  const PAD_X = 36;
  const COLS = 24;
  const VB_W = 480;
  const VB_H = HEAD_H + DAYS.length * ROW_H + 8;
  const colW = (VB_W - PAD_X) / COLS;
  const xFor = (h: number) => PAD_X + h * colW;
  const liveRowIdx = DAYS.findIndex((d) => d.value === live.day);

  return (
    <Panel>
      <PanelHeader>
        <h3 className="text-sm font-semibold">{t("admin:quietHours.timeline.title")}</h3>
        {enabled ? (
          <StatusPill status="info">{t("admin:quietHours.status.live")}</StatusPill>
        ) : (
          <StatusPill status="offline">{t("admin:quietHours.status.disabled")}</StatusPill>
        )}
      </PanelHeader>
      <PanelBody className="space-y-3">
        <div className="overflow-x-auto">
          <svg
            viewBox={`0 0 ${VB_W} ${VB_H}`}
            preserveAspectRatio="none"
            role="img"
            aria-label={t("admin:quietHours.timeline.ariaLabel")}
            className="block h-[180px] w-full min-w-[420px]"
          >
            {/* Hour ticks header */}
            {Array.from({ length: COLS + 1 }, (_, h) => (
              <g key={`hh-${h}`}>
                <line
                  x1={xFor(h)}
                  x2={xFor(h)}
                  y1={HEAD_H - 4}
                  y2={VB_H - 4}
                  stroke="currentColor"
                  className="text-border"
                  strokeWidth={h % 6 === 0 ? 0.6 : 0.3}
                  opacity={h % 6 === 0 ? 0.8 : 0.4}
                />
                {h % 6 === 0 && h <= 24 && (
                  <text
                    x={xFor(h)}
                    y={HEAD_H - 6}
                    fontSize={8}
                    textAnchor="middle"
                    className="fill-fg-subtle font-mono"
                  >
                    {String(h).padStart(2, "0")}
                  </text>
                )}
              </g>
            ))}

            {/* Per-day rows */}
            {rows.map((row, idx) => {
              const y = HEAD_H + idx * ROW_H;
              const isLiveRow = enabled && row.value === live.day;
              return (
                <g key={row.value}>
                  <text
                    x={4}
                    y={y + ROW_H / 2 + 3}
                    fontSize={9}
                    className={`font-mono ${isLiveRow ? "fill-fg" : "fill-fg-muted"}`}
                  >
                    {row.short}
                  </text>
                  <rect
                    x={PAD_X}
                    y={y + 3}
                    width={VB_W - PAD_X}
                    height={ROW_H - 6}
                    className="fill-panel-2"
                  />
                  {row.ranges.map((r, i) => (
                    <rect
                      key={i}
                      x={xFor(r.from)}
                      y={y + 3}
                      width={Math.max(0, xFor(r.to) - xFor(r.from))}
                      height={ROW_H - 6}
                      className="fill-accent/40"
                      stroke="currentColor"
                      strokeWidth={0.3}
                    />
                  ))}
                </g>
              );
            })}

            {/* Live "now" indicator — vertical accent line on the active day */}
            {enabled && liveRowIdx >= 0 && (
              <line
                x1={xFor(Math.min(24, Math.max(0, live.hour)))}
                x2={xFor(Math.min(24, Math.max(0, live.hour)))}
                y1={HEAD_H + liveRowIdx * ROW_H + 1}
                y2={HEAD_H + (liveRowIdx + 1) * ROW_H - 1}
                className="stroke-accent"
                strokeWidth={1.4}
              />
            )}
          </svg>
        </div>

        <div className="flex flex-wrap items-center gap-3 text-xs text-fg-subtle">
          <span className="inline-flex items-center gap-1.5">
            <span className="inline-block h-3 w-3 rounded-sm bg-accent/40 ring-1 ring-inset ring-accent/30" />
            {t("admin:quietHours.timeline.legendQuiet")}
          </span>
          <span className="inline-flex items-center gap-1.5">
            <span className="inline-block h-3 w-3 rounded-sm bg-panel-2 ring-1 ring-inset ring-border" />
            {t("admin:quietHours.timeline.legendDelivering")}
          </span>
          <span className="inline-flex items-center gap-1.5">
            <span className="inline-block h-3 w-px bg-accent" />{" "}
            {t("admin:quietHours.timeline.legendNow", { tz })}
          </span>
        </div>

        <div className="flex flex-wrap items-center gap-3 border-t border-border pt-3">
          <Button
            type="button"
            onClick={() => setPreviewAt(new Date())}
            disabled={!enabled}
            title={
              !enabled
                ? t("admin:quietHours.timeline.testNowHintDisabled")
                : t("admin:quietHours.timeline.testNowHintReady")
            }
          >
            <PlayCircle className="h-3.5 w-3.5" />
            {t("admin:quietHours.timeline.testNow")}
          </Button>
          {previewQuiet === null ? (
            <span className="text-xs text-fg-subtle">
              {t("admin:quietHours.timeline.previewHint")}
              <span className="font-medium text-fg">
                {t("admin:quietHours.timeline.previewHintAction")}
              </span>
              {t("admin:quietHours.timeline.previewHintTail")}
            </span>
          ) : previewQuiet ? (
            <span className="inline-flex flex-wrap items-center gap-2 text-sm">
              <StatusPill status="info">{t("admin:quietHours.timeline.muted")}</StatusPill>
              <span className="text-fg-muted">
                {t("admin:quietHours.timeline.mutedMsg", {
                  when: previewAt!.toLocaleString(),
                  tz,
                })}
              </span>
            </span>
          ) : (
            <span className="inline-flex flex-wrap items-center gap-2 text-sm">
              <StatusPill status="ok">{t("admin:quietHours.timeline.delivering")}</StatusPill>
              <span className="text-fg-muted">
                {t("admin:quietHours.timeline.deliveringMsg", {
                  when: previewAt!.toLocaleString(),
                  tz,
                })}
              </span>
            </span>
          )}
        </div>
      </PanelBody>
    </Panel>
  );
}
