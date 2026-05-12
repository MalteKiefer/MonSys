import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { Activity, Clock, PencilLine, Plus, Trash2, X } from "lucide-react";
import type { FormEvent} from "react";
import { useEffect, useMemo, useState } from "react";

import type { ChartSeries} from "../components/Chart";
import { ChartLine, colorFor } from "../components/Chart";
import { Page } from "../components/page";
import type {
  TabItem} from "../components/ui";
import {
  Button,
  Empty,
  ErrorBox,
  Field,
  Panel,
  PanelBody,
  PanelHeader,
  StatusPill,
  TBody,
  TD,
  TH,
  THead,
  Table,
  Tabs,
  TextInput,
  TimeRangeSelector,
} from "../components/ui";
import { useT } from "../i18n/useT";
import { api, ApiError } from "../lib/api";
import type { HostGroup, Monitor, MonitorInput, MonitorResult } from "../lib/types";

type TabKey = "active" | "create" | "history";

const TYPES: Monitor["type"][] = ["cert", "postgres", "mysql", "mongodb", "http", "tcp"];

// Hint keys mapped per monitor type; the actual translated text comes from
// admin:monitors.target_hint.* and admin:monitors.type_fields.* at render time.
const TARGET_HINT_KEYS: Record<Monitor["type"], string> = {
  cert: "admin:monitors.target_hint.cert",
  postgres: "admin:monitors.target_hint.postgres",
  mysql: "admin:monitors.target_hint.mysql",
  mongodb: "admin:monitors.target_hint.mongodb",
  http: "admin:monitors.target_hint.http",
  tcp: "admin:monitors.target_hint.tcp",
};

const TYPE_FIELDS: Partial<
  Record<Monitor["type"], { key: string; labelKey: string; placeholder?: string }[]>
> = {
  cert: [
    { key: "warn_days", labelKey: "admin:monitors.type_fields.warn_days", placeholder: "30" },
    { key: "fail_days", labelKey: "admin:monitors.type_fields.fail_days", placeholder: "7" },
    { key: "server_name", labelKey: "admin:monitors.type_fields.server_name" },
  ],
  http: [
    { key: "expected_status", labelKey: "admin:monitors.type_fields.expected_status", placeholder: "200" },
    { key: "method", labelKey: "admin:monitors.type_fields.method", placeholder: "GET" },
  ],
};

// SidePanelMode encodes which view is shown inside the slide-over: a brand-new
// monitor (mode="create"), or an existing monitor's detail+edit pane.
type SidePanelMode =
  | { kind: "closed" }
  | { kind: "create" }
  | { kind: "detail"; monitor: Monitor };

export function AdminMonitors() {
  const { t } = useT(["admin", "common"]);
  const qc = useQueryClient();
  const list = useQuery({
    queryKey: ["monitors"],
    queryFn: () => api<{ monitors: Monitor[] }>("/v1/monitors"),
    refetchInterval: 30_000,
  });

  const [panel, setPanel] = useState<SidePanelMode>({ kind: "closed" });
  const [tab, setTab] = useState<TabKey>("active");

  // Keep the side-panel monitor in sync with the latest list data so live
  // status/latency updates while the panel is open. Lookup by id.
  const liveSelected = useMemo(() => {
    if (panel.kind !== "detail") return null;
    return (list.data?.monitors ?? []).find((m) => m.id === panel.monitor.id) ?? panel.monitor;
  }, [panel, list.data?.monitors]);

  function close() {
    setPanel({ kind: "closed" });
  }

  // Close on Escape — standard slide-over UX.
  useEffect(() => {
    if (panel.kind === "closed") return;
    function onKey(e: KeyboardEvent) {
      if (e.key === "Escape") close();
    }
    window.addEventListener("keydown", onKey);
    return () => { window.removeEventListener("keydown", onKey); };
  }, [panel.kind]);

  const monitors = list.data?.monitors ?? [];

  const tabs: readonly TabItem<TabKey>[] = [
    { key: "active", label: t("admin:monitors.tabs.active"), icon: Activity, badge: monitors.length || undefined },
    { key: "create", label: t("admin:monitors.tabs.create"), icon: Plus },
    { key: "history", label: t("admin:monitors.tabs.history"), icon: Clock },
  ];

  return (
    <Page title={t("admin:monitors.title")} subtitle={t("admin:monitors.subtitle")}>
      <Tabs items={tabs} value={tab} onChange={setTab} />

      <div
        role="tabpanel"
        id={`panel-${tab}`}
        aria-labelledby={`tab-${tab}`}
        className="mt-3"
      >
        {tab === "active" && (
          <ActiveMonitorsTab
            monitors={monitors}
            isLoading={list.isLoading}
            onOpenDetail={(m) => { setPanel({ kind: "detail", monitor: m }); }}
            onCreate={() => { setTab("create"); }}
            onDelete={(m) => {
              if (confirm(t("admin:monitors.active.delete_confirm", { name: m.name })))
                void api(`/v1/monitors/${m.id}`, { method: "DELETE" }).then(() =>
                  qc.invalidateQueries({ queryKey: ["monitors"] }),
                );
            }}
          />
        )}

        {tab === "create" && (
          <CreateMonitorTab
            onCreated={() => {
              void qc.invalidateQueries({ queryKey: ["monitors"] });
              setTab("active");
            }}
            onCancel={() => { setTab("active"); }}
          />
        )}

        {tab === "history" && (
          <RecentResultsTab monitors={monitors} isLoading={list.isLoading} />
        )}
      </div>

      {panel.kind !== "closed" && (
        <SlideOver
          onClose={close}
          title={panel.kind === "create" ? t("admin:monitors.slide_over.new_title") : liveSelected?.name ?? ""}
        >
          {panel.kind === "create" ? (
            <MonitorForm
              initial={null}
              onCancel={close}
              onSaved={() => {
                void qc.invalidateQueries({ queryKey: ["monitors"] });
                close();
              }}
            />
          ) : liveSelected ? (
            <MonitorDetail
              monitor={liveSelected}
              onSaved={() => { void qc.invalidateQueries({ queryKey: ["monitors"] }); }}
              onClose={close}
            />
          ) : null}
        </SlideOver>
      )}
    </Page>
  );
}

// ---- Tab: Active monitors ------------------------------------------------

// Lists every running probe in a dense table. Row click opens the slide-over
// detail/edit pane (preserves the modal flow the original page used). The
// "+ Create monitor" button is in-tab — the page-level action slot is now
// owned by the Tabs strip, so the create CTA lives next to the data it adds.
function ActiveMonitorsTab({
  monitors,
  isLoading,
  onOpenDetail,
  onCreate,
  onDelete,
}: {
  monitors: Monitor[];
  isLoading: boolean;
  onOpenDetail: (m: Monitor) => void;
  onCreate: () => void;
  onDelete: (m: Monitor) => void;
}) {
  const { t } = useT(["admin", "common"]);
  return (
    <Panel>
      <PanelHeader>
        <div className="flex items-center gap-3">
          <h3 className="text-sm font-semibold">{t("admin:monitors.active.heading")}</h3>
          <span className="text-xs tabular-nums text-fg-subtle">
            {t("admin:monitors.active.total_count", { count: monitors.length })}
          </span>
        </div>
        <Button variant="primary" size="sm" onClick={onCreate}>
          <Plus className="h-3.5 w-3.5" /> {t("admin:monitors.active.create_button")}
        </Button>
      </PanelHeader>
      <PanelBody className="p-0 overflow-x-auto">
        {isLoading ? (
          <p className="px-5 py-4 text-sm text-fg-subtle">{t("common:actions.loading")}</p>
        ) : monitors.length === 0 ? (
          <p className="px-5 py-8 text-center text-sm text-fg-subtle">{t("admin:monitors.active.empty")}</p>
        ) : (
          <Table>
            <THead>
              <tr>
                <TH>{t("admin:monitors.active.col_type")}</TH>
                <TH>{t("admin:monitors.active.col_name")}</TH>
                <TH>{t("admin:monitors.active.col_target")}</TH>
                <TH>{t("admin:monitors.active.col_interval")}</TH>
                <TH>{t("admin:monitors.active.col_status")}</TH>
                <TH>{t("admin:monitors.active.col_latency")}</TH>
                <TH>{t("admin:monitors.active.col_last_detail")}</TH>
                <TH className="text-right">{t("admin:monitors.active.col_actions")}</TH>
              </tr>
            </THead>
            <TBody>
              {monitors.map((m) => (
                <tr
                  key={m.id}
                  role="button"
                  tabIndex={0}
                  onClick={() => { onOpenDetail(m); }}
                  onKeyDown={(e) => {
                    if (e.key === "Enter" || e.key === " ") {
                      e.preventDefault();
                      onOpenDetail(m);
                    }
                  }}
                  className="cursor-pointer hover:bg-panel-2 focus:outline-none focus-visible:bg-panel-2 focus-visible:ring-2 focus-visible:ring-inset focus-visible:ring-accent/50"
                >
                  <TD className="text-fg-muted">{m.type}</TD>
                  <TD className="font-medium">{m.name}</TD>
                  <TD className="font-mono text-xs text-fg-muted truncate max-w-xs">{m.target}</TD>
                  <TD className="tabular-nums text-fg-muted">{m.interval_sec}s</TD>
                  <TD>
                    <StatusPill status={m.last_status ?? "unknown"}>{m.last_status ?? "?"}</StatusPill>
                  </TD>
                  <TD className="tabular-nums text-fg-muted">
                    {m.last_latency_ms ? `${m.last_latency_ms} ms` : "—"}
                  </TD>
                  <TD className="font-mono text-xs text-fg-subtle truncate max-w-xs">{m.last_detail ?? "—"}</TD>
                  <TD className="text-right">
                    {/* Stop row-click propagation here so action buttons
                        don't double-fire the row's open-detail handler. */}
                    <div
                      className="inline-flex items-center gap-1"
                      onClick={(e) => { e.stopPropagation(); }}
                    >
                      <Button onClick={() => { onOpenDetail(m); }}>
                        <PencilLine className="h-3.5 w-3.5" /> {t("common:actions.edit")}
                      </Button>
                      <Button variant="danger" onClick={() => { onDelete(m); }}>
                        <Trash2 className="h-3.5 w-3.5" />
                      </Button>
                    </div>
                  </TD>
                </tr>
              ))}
            </TBody>
          </Table>
        )}
      </PanelBody>
    </Panel>
  );
}

// ---- Tab: Create monitor -------------------------------------------------

// Hosts the same MonitorForm used inside the slide-over, but rendered inline
// in its own tab for a more focused entry experience. Saving switches back to
// the "active" tab so the freshly created monitor is visible immediately.
function CreateMonitorTab({
  onCreated,
  onCancel,
}: {
  onCreated: () => void;
  onCancel: () => void;
}) {
  const { t } = useT(["admin", "common"]);
  return (
    <Panel>
      <PanelHeader>
        <h3 className="text-sm font-semibold">{t("admin:monitors.create.heading")}</h3>
        <span className="text-xs text-fg-subtle">{t("admin:monitors.create.hint")}</span>
      </PanelHeader>
      <PanelBody>
        <MonitorForm initial={null} onCancel={onCancel} onSaved={onCreated} />
      </PanelBody>
    </Panel>
  );
}

// ---- Tab: Recent results -------------------------------------------------

// Aggregated, read-only snapshot of the latest probe outcome per monitor. We
// use the data already loaded for the "active" tab (each Monitor row carries
// its most recent status/latency/detail) so no extra request is needed.
// Rows are sorted with failures first, then warnings, then unknown, then OK,
// giving the operator an at-a-glance triage view.
const STATUS_RANK: Record<string, number> = {
  fail: 0,
  warn: 1,
  unknown: 2,
  ok: 3,
};

function RecentResultsTab({
  monitors,
  isLoading,
}: {
  monitors: Monitor[];
  isLoading: boolean;
}) {
  const { t } = useT(["admin", "common"]);
  const sorted = useMemo(() => {
    const copy = monitors.slice();
    copy.sort((a, b) => {
      const ra = STATUS_RANK[a.last_status ?? "unknown"] ?? 2;
      const rb = STATUS_RANK[b.last_status ?? "unknown"] ?? 2;
      if (ra !== rb) return ra - rb;
      return a.name.localeCompare(b.name);
    });
    return copy;
  }, [monitors]);

  const okCount = monitors.filter((m) => m.last_status === "ok").length;
  const warnCount = monitors.filter((m) => m.last_status === "warn").length;
  const failCount = monitors.filter((m) => m.last_status === "fail").length;
  const unknownCount = monitors.length - okCount - warnCount - failCount;

  return (
    <Panel>
      <PanelHeader>
        <div className="flex items-center gap-3">
          <h3 className="text-sm font-semibold">{t("admin:monitors.history.heading")}</h3>
          <span className="text-xs tabular-nums text-fg-subtle">
            {t("admin:monitors.history.subheading")}
          </span>
        </div>
        <div className="flex items-center gap-2 text-[11px] tabular-nums">
          <StatusPill status="fail">{t("admin:monitors.history.fail_count", { count: failCount })}</StatusPill>
          <StatusPill status="warn">{t("admin:monitors.history.warn_count", { count: warnCount })}</StatusPill>
          <StatusPill status="unknown">{t("admin:monitors.history.unknown_count", { count: unknownCount })}</StatusPill>
          <StatusPill status="ok">{t("admin:monitors.history.ok_count", { count: okCount })}</StatusPill>
        </div>
      </PanelHeader>
      <PanelBody className="p-0 overflow-x-auto">
        {isLoading ? (
          <p className="px-5 py-4 text-sm text-fg-subtle">{t("common:actions.loading")}</p>
        ) : sorted.length === 0 ? (
          <p className="px-5 py-8 text-center text-sm text-fg-subtle">{t("admin:monitors.history.empty")}</p>
        ) : (
          <Table>
            <THead>
              <tr>
                <TH>{t("admin:monitors.history.col_status")}</TH>
                <TH>{t("admin:monitors.history.col_name")}</TH>
                <TH>{t("admin:monitors.history.col_type")}</TH>
                <TH>{t("admin:monitors.history.col_target")}</TH>
                <TH>{t("admin:monitors.history.col_latency")}</TH>
                <TH>{t("admin:monitors.history.col_detail")}</TH>
              </tr>
            </THead>
            <TBody>
              {sorted.map((m) => (
                <tr key={m.id}>
                  <TD>
                    <StatusPill status={m.last_status ?? "unknown"}>
                      {m.last_status ?? "?"}
                    </StatusPill>
                  </TD>
                  <TD className="font-medium">{m.name}</TD>
                  <TD className="text-fg-muted">{m.type}</TD>
                  <TD className="font-mono text-xs text-fg-muted truncate max-w-xs">{m.target}</TD>
                  <TD className="tabular-nums text-fg-muted">
                    {m.last_latency_ms ? `${m.last_latency_ms} ms` : "—"}
                  </TD>
                  <TD className="font-mono text-xs text-fg-subtle truncate max-w-md">
                    {m.last_detail ?? "—"}
                  </TD>
                </tr>
              ))}
            </TBody>
          </Table>
        )}
      </PanelBody>
    </Panel>
  );
}

// ---- Slide-over shell ----------------------------------------------------

// SlideOver renders a right-anchored panel overlay. We avoid pulling in a
// modal/dialog dependency — this is a lean, accessible (Esc + backdrop click)
// implementation tailored to the few places we need it.
function SlideOver({
  title,
  onClose,
  children,
}: {
  title: string;
  onClose: () => void;
  children: React.ReactNode;
}) {
  const { t } = useT(["admin", "common"]);
  return (
    <div
      className="fixed inset-0 z-50 flex"
      role="dialog"
      aria-modal="true"
      aria-label={title}
    >
      <button
        type="button"
        aria-label={t("admin:monitors.slide_over.close_aria")}
        onClick={onClose}
        className="flex-1 cursor-default bg-bg/60 backdrop-blur-sm transition-opacity duration-150"
      />
      <aside className="flex w-full max-w-md flex-col border-l border-border bg-panel shadow-2xl">
        <header className="flex items-center justify-between border-b border-border px-5 py-3">
          <h2 className="truncate text-sm font-semibold text-fg">{title}</h2>
          <button
            type="button"
            onClick={onClose}
            aria-label={t("admin:monitors.slide_over.close_panel_aria")}
            className="rounded p-1 text-fg-subtle hover:bg-panel-2 hover:text-fg"
          >
            <X className="h-4 w-4" />
          </button>
        </header>
        <div className="flex-1 overflow-y-auto px-5 py-4">{children}</div>
      </aside>
    </div>
  );
}

// ---- Detail (inside slide-over) ------------------------------------------

// MonitorDetail bundles the at-a-glance status block, the editable form, and
// the recent results chart inside the slide-over. Splitting these into nested
// components kept the tree readable while still allowing the form to live
// alongside the headline data.
function MonitorDetail({
  monitor,
  onSaved,
  onClose,
}: {
  monitor: Monitor;
  onSaved: () => void;
  onClose: () => void;
}) {
  const { t } = useT(["admin", "common"]);
  return (
    <div className="space-y-5">
      <div className="grid grid-cols-2 gap-3 text-sm">
        <DetailRow label={t("admin:monitors.detail.type")} value={<span className="text-fg-muted">{monitor.type}</span>} />
        <DetailRow
          label={t("admin:monitors.detail.status")}
          value={<StatusPill status={monitor.last_status ?? "unknown"}>{monitor.last_status ?? "?"}</StatusPill>}
        />
        <DetailRow label={t("admin:monitors.detail.interval")} value={<span className="tabular-nums">{monitor.interval_sec}s</span>} />
        <DetailRow
          label={t("admin:monitors.detail.latency")}
          value={
            <span className="tabular-nums text-fg-muted">
              {monitor.last_latency_ms ? `${monitor.last_latency_ms} ms` : "—"}
            </span>
          }
        />
        <DetailRow
          label={t("admin:monitors.detail.target")}
          value={<span className="break-all font-mono text-xs text-fg-muted">{monitor.target}</span>}
          full
        />
        {monitor.last_detail && (
          <DetailRow
            label={t("admin:monitors.detail.last_detail")}
            value={<span className="break-all font-mono text-xs text-fg-subtle">{monitor.last_detail}</span>}
            full
          />
        )}
      </div>

      <ResultsBlock monitor={monitor} />

      <div>
        <h3 className="mb-2 text-[11px] font-semibold uppercase tracking-wider text-fg-subtle">
          {t("admin:monitors.detail.edit_heading")}
        </h3>
        <MonitorForm
          initial={monitor}
          onCancel={onClose}
          onSaved={() => {
            onSaved();
            onClose();
          }}
        />
      </div>
    </div>
  );
}

function DetailRow({
  label,
  value,
  full,
}: {
  label: string;
  value: React.ReactNode;
  full?: boolean;
}) {
  return (
    <div className={full ? "col-span-2" : ""}>
      <p className="text-[11px] font-medium uppercase tracking-wider text-fg-subtle">{label}</p>
      <div className="mt-0.5">{value}</div>
    </div>
  );
}

// ---- Form ----------------------------------------------------------------

function MonitorForm({
  initial,
  onCancel,
  onSaved,
}: {
  initial: Monitor | null;
  onCancel: () => void;
  onSaved: () => void;
}) {
  const { t } = useT(["admin", "common"]);
  const tagsQuery = useQuery({
    queryKey: ["tags"],
    queryFn: () => api<{ tags: { tag: string; count: number }[] }>("/v1/tags"),
  });
  const groupsQuery = useQuery({
    queryKey: ["groups"],
    queryFn: () => api<{ groups: HostGroup[] }>("/v1/groups"),
  });
  const [type, setType] = useState<Monitor["type"]>(initial?.type ?? "http");
  const [name, setName] = useState(initial?.name ?? "");
  const [target, setTarget] = useState(initial?.target ?? "");
  const [intervalSec, setIntervalSec] = useState(initial?.interval_sec ?? 60);
  const [enabled, setEnabled] = useState(initial?.enabled ?? true);
  const [params, setParams] = useState<Record<string, string>>(() => {
    const out: Record<string, string> = {};
    if (initial?.params) for (const [k, v] of Object.entries(initial.params)) out[k] = String(v ?? "");
    return out;
  });
  const [tagsRaw, setTagsRaw] = useState((initial?.target_tags ?? []).join(", "));
  const [groupIDs, setGroupIDs] = useState<string[]>(initial?.target_group_ids ?? []);
  const [error, setError] = useState<string | null>(null);

  const save = useMutation({
    mutationFn: () => {
      const parsedParams: Record<string, unknown> = {};
      for (const [k, v] of Object.entries(params)) {
        if (v === "") continue;
        const n = Number(v);
        parsedParams[k] = Number.isFinite(n) && /^\d+$/.test(v) ? n : v;
      }
      const tagList = tagsRaw
        .split(",")
        .map((s) => s.trim().toLowerCase())
        .filter(Boolean);
      const body: MonitorInput = {
        type,
        name,
        target,
        interval_sec: intervalSec,
        enabled,
        params: parsedParams,
        target_tags: tagList,
        target_group_ids: groupIDs,
      };
      if (initial) {
        return api(`/v1/monitors/${initial.id}`, {
          method: "PUT",
          body: JSON.stringify(body),
        });
      }
      return api("/v1/monitors", {
        method: "POST",
        body: JSON.stringify(body),
      });
    },
    onSuccess: onSaved,
    onError: (err) => { setError(err instanceof ApiError ? err.detail : t("admin:monitors.form.failed")); },
  });

  function submit(e: FormEvent) {
    e.preventDefault();
    setError(null);
    save.mutate();
  }

  const fields = TYPE_FIELDS[type] ?? [];

  return (
    <form onSubmit={submit} className="space-y-4">
      <div className="grid grid-cols-2 gap-3">
        <Field label={t("admin:monitors.form.type")}>
          <select
            value={type}
            disabled={!!initial}
            onChange={(e) => {
              setType(e.target.value as Monitor["type"]);
              setParams({});
            }}
            className="w-full rounded-md border border-border bg-panel px-3 py-2 text-sm disabled:opacity-60"
          >
            {TYPES.map((typeOpt) => (
              <option key={typeOpt} value={typeOpt}>
                {typeOpt}
              </option>
            ))}
          </select>
        </Field>
        <Field label={t("admin:monitors.form.name")}>
          <TextInput required value={name} onChange={(e) => { setName(e.target.value); }} />
        </Field>
      </div>

      <Field label={t("admin:monitors.form.target")} hint={t(TARGET_HINT_KEYS[type])}>
        <TextInput required value={target} onChange={(e) => { setTarget(e.target.value); }} className="font-mono" />
      </Field>

      {fields.length > 0 && (
        <div className="grid grid-cols-2 gap-3">
          {fields.map((f) => (
            <Field key={f.key} label={t(f.labelKey)}>
              <TextInput
                placeholder={f.placeholder}
                value={params[f.key] ?? ""}
                onChange={(e) => { setParams({ ...params, [f.key]: e.target.value }); }}
              />
            </Field>
          ))}
        </div>
      )}

      <div className="grid grid-cols-2 gap-3">
        <Field label={t("admin:monitors.form.interval_seconds")}>
          <TextInput
            type="number"
            min={10}
            max={86400}
            value={intervalSec}
            onChange={(e) => { setIntervalSec(parseInt(e.target.value || "60", 10)); }}
          />
        </Field>
        <Field label={t("admin:monitors.form.enabled")}>
          <label className="mt-2 inline-flex items-center gap-2 text-sm text-fg-muted">
            <input type="checkbox" checked={enabled} onChange={(e) => { setEnabled(e.target.checked); }} />
            {t("admin:monitors.form.on")}
          </label>
        </Field>
      </div>

      <Field
        label={t("admin:monitors.form.tags_label")}
        hint={
          tagsQuery.data?.tags?.length
            ? t("admin:monitors.form.tags_existing_hint", {
                tags: tagsQuery.data.tags.map((tag) => tag.tag).join(", "),
              })
            : t("admin:monitors.form.tags_none_hint")
        }
      >
        <TextInput
          value={tagsRaw}
          onChange={(e) => { setTagsRaw(e.target.value); }}
          placeholder={t("admin:monitors.form.tags_placeholder")}
          className="font-mono"
        />
      </Field>

      <Field label={t("admin:monitors.form.groups_label")}>
        <select
          multiple
          size={Math.min(5, Math.max(2, groupsQuery.data?.groups.length ?? 2))}
          value={groupIDs}
          onChange={(e) =>
            { setGroupIDs(Array.from(e.target.selectedOptions).map((o) => o.value)); }
          }
          className="w-full rounded-md border border-border bg-panel px-3 py-2 text-sm"
        >
          {(groupsQuery.data?.groups ?? []).map((g) => (
            <option key={g.id} value={g.id}>
              {g.name} ({g.member_ids.length})
            </option>
          ))}
        </select>
      </Field>

      {error && <ErrorBox>{error}</ErrorBox>}

      <div className="flex items-center gap-2">
        <Button variant="primary" type="submit" disabled={save.isPending}>
          {save.isPending
            ? t("common:actions.saving")
            : initial
              ? t("common:actions.save")
              : t("common:actions.create")}
        </Button>
        <Button type="button" onClick={onCancel}>
          {t("common:actions.cancel")}
        </Button>
      </div>
    </form>
  );
}

// ---- Results block (inside slide-over) -----------------------------------

function ResultsBlock({ monitor }: { monitor: Monitor }) {
  const { t } = useT(["admin", "common"]);
  const [rangeSec, setRangeSec] = useState(60 * 60);
  const sinceISO = useMemo(
    () => new Date(Date.now() - rangeSec * 1000).toISOString(),
    [rangeSec],
  );
  const results = useQuery({
    queryKey: ["monitor-results", monitor.id, rangeSec],
    queryFn: () =>
      api<{ monitor_id: string; results: MonitorResult[] }>(
        `/v1/monitors/${monitor.id}/results?since=${encodeURIComponent(sinceISO)}&limit=1000`,
      ),
    refetchInterval: 30_000,
  });

  const samples = (results.data?.results ?? []).slice().reverse();

  const matrix = useMemo(() => {
    const t = samples.map((r) => Math.floor(new Date(r.time).getTime() / 1000));
    const lat = samples.map((r) => r.latency_ms);
    return [t, lat];
  }, [samples]);

  const series: ChartSeries[] = [
    { label: t("admin:monitors.results.latency_series"), color: colorFor(0), fill: "rgba(16,185,129,0.10)" },
  ];

  const okCount = samples.filter((r) => r.status === "ok").length;
  const failCount = samples.filter((r) => r.status === "fail").length;
  const warnCount = samples.filter((r) => r.status === "warn").length;
  const uptimePct = samples.length > 0 ? (okCount / samples.length) * 100 : 0;

  return (
    <div>
      <div className="mb-2 flex items-center justify-between">
        <h3 className="text-[11px] font-semibold uppercase tracking-wider text-fg-subtle">
          <Activity className="mr-1 inline h-3 w-3" />
          {t("admin:monitors.results.heading")}
        </h3>
        <TimeRangeSelector value={rangeSec} onChange={setRangeSec} />
      </div>
      {samples.length === 0 ? (
        <Empty>{t("admin:monitors.results.empty_range")}</Empty>
      ) : (
        <>
          <div className="grid grid-cols-4 gap-2 text-xs">
            <SmallStat label={t("admin:monitors.results.samples")} value={samples.length} />
            <SmallStat label={t("admin:monitors.results.ok")} value={okCount} tone="ok" />
            <SmallStat label={t("admin:monitors.results.warn")} value={warnCount} tone="warn" />
            <SmallStat label={t("admin:monitors.results.fail")} value={failCount} tone="fail" />
          </div>
          <p className="mt-2 text-xs text-fg-subtle tabular-nums">
            {t("admin:monitors.results.uptime", { pct: uptimePct.toFixed(1) })}
          </p>
          <div className="mt-3">
            <ChartLine
              data={{ matrix }}
              series={series}
              formatY={(v) => `${v.toFixed(0)} ms`}
              yMin={0}
              height={160}
            />
          </div>
        </>
      )}
    </div>
  );
}

function SmallStat({ label, value, tone }: { label: string; value: number; tone?: "ok" | "warn" | "fail" }) {
  const color = tone === "ok" ? "text-ok" : tone === "warn" ? "text-warn" : tone === "fail" ? "text-fail" : "text-fg";
  return (
    <div className="rounded-md border border-border bg-panel-2 p-2">
      <p className="text-[10px] uppercase tracking-wider text-fg-subtle">{label}</p>
      <p className={`mt-0.5 text-lg font-semibold tabular-nums ${color}`}>{value}</p>
    </div>
  );
}
