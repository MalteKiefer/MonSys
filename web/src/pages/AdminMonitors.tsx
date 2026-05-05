import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { Activity, PencilLine, Plus, Trash2, X } from "lucide-react";
import { FormEvent, useEffect, useMemo, useState } from "react";

import { ChartLine, ChartSeries, colorFor } from "../components/Chart";
import { Page } from "../components/page";
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
  TextInput,
  TimeRangeSelector,
} from "../components/ui";
import { api, ApiError } from "../lib/api";
import { HostGroup, Monitor, MonitorInput, MonitorResult } from "../lib/types";

const TYPES: Monitor["type"][] = ["cert", "postgres", "mysql", "mongodb", "http", "tcp"];

const TARGET_HINT: Record<Monitor["type"], string> = {
  cert: "host:port (e.g. example.com:443)",
  postgres: "DSN, e.g. postgres://user:pw@host:5432/db?sslmode=require",
  mysql: "host:port (handshake-only, no auth)",
  mongodb: "host:port (hello check)",
  http: "URL, e.g. https://example.com/health",
  tcp: "host:port",
};

const TYPE_FIELDS: Partial<Record<Monitor["type"], Array<{ key: string; label: string; placeholder?: string }>>> = {
  cert: [
    { key: "warn_days", label: "Warn (days)", placeholder: "30" },
    { key: "fail_days", label: "Fail (days)", placeholder: "7" },
    { key: "server_name", label: "SNI server name (optional)" },
  ],
  http: [
    { key: "expected_status", label: "Expected status", placeholder: "200" },
    { key: "method", label: "Method", placeholder: "GET" },
  ],
};

// SidePanelMode encodes which view is shown inside the slide-over: a brand-new
// monitor (mode="create"), or an existing monitor's detail+edit pane.
type SidePanelMode =
  | { kind: "closed" }
  | { kind: "create" }
  | { kind: "detail"; monitor: Monitor };

export function AdminMonitors() {
  const qc = useQueryClient();
  const list = useQuery({
    queryKey: ["monitors"],
    queryFn: () => api<{ monitors: Monitor[] }>("/v1/monitors"),
    refetchInterval: 30_000,
  });

  const [panel, setPanel] = useState<SidePanelMode>({ kind: "closed" });

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
    return () => window.removeEventListener("keydown", onKey);
  }, [panel.kind]);

  const addBtn = (
    <Button variant="primary" onClick={() => setPanel({ kind: "create" })}>
      <Plus className="h-3.5 w-3.5" /> Add monitor
    </Button>
  );

  const subtitle = "Server-side periodic probes: cert expiry, DB reachability, HTTP, raw TCP.";

  return (
    <Page title="Monitors" subtitle={subtitle} actions={addBtn}>
      <Panel>
        <PanelHeader>
          <h3 className="text-sm font-semibold">All monitors</h3>
          <span className="text-xs tabular-nums text-fg-subtle">
            {(list.data?.monitors ?? []).length} total
          </span>
        </PanelHeader>
        <PanelBody className="p-0 overflow-x-auto">
          {list.isLoading ? (
            <p className="px-5 py-4 text-sm text-fg-subtle">Loading…</p>
          ) : (list.data?.monitors ?? []).length === 0 ? (
            <p className="px-5 py-8 text-center text-sm text-fg-subtle">No monitors yet.</p>
          ) : (
            <Table>
              <THead>
                <tr>
                  <TH>Type</TH>
                  <TH>Name</TH>
                  <TH>Target</TH>
                  <TH>Interval</TH>
                  <TH>Status</TH>
                  <TH>Latency</TH>
                  <TH>Last detail</TH>
                  <TH className="text-right">Actions</TH>
                </tr>
              </THead>
              <TBody>
                {(list.data?.monitors ?? []).map((m) => (
                  <tr
                    key={m.id}
                    role="button"
                    tabIndex={0}
                    onClick={() => setPanel({ kind: "detail", monitor: m })}
                    onKeyDown={(e) => {
                      if (e.key === "Enter" || e.key === " ") {
                        e.preventDefault();
                        setPanel({ kind: "detail", monitor: m });
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
                        onClick={(e) => e.stopPropagation()}
                      >
                        <Button onClick={() => setPanel({ kind: "detail", monitor: m })}>
                          <PencilLine className="h-3.5 w-3.5" /> Edit
                        </Button>
                        <Button
                          variant="danger"
                          onClick={() => {
                            if (confirm(`Delete monitor "${m.name}"?`))
                              api(`/v1/monitors/${m.id}`, { method: "DELETE" }).then(() =>
                                qc.invalidateQueries({ queryKey: ["monitors"] }),
                              );
                          }}
                        >
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

      {panel.kind !== "closed" && (
        <SlideOver onClose={close} title={panel.kind === "create" ? "New monitor" : liveSelected?.name ?? ""}>
          {panel.kind === "create" ? (
            <MonitorForm
              initial={null}
              onCancel={close}
              onSaved={() => {
                qc.invalidateQueries({ queryKey: ["monitors"] });
                close();
              }}
            />
          ) : liveSelected ? (
            <MonitorDetail
              monitor={liveSelected}
              onSaved={() => qc.invalidateQueries({ queryKey: ["monitors"] })}
              onClose={close}
            />
          ) : null}
        </SlideOver>
      )}
    </Page>
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
  return (
    <div
      className="fixed inset-0 z-50 flex"
      role="dialog"
      aria-modal="true"
      aria-label={title}
    >
      <button
        type="button"
        aria-label="Close"
        onClick={onClose}
        className="flex-1 cursor-default bg-bg/60 backdrop-blur-sm transition-opacity duration-150"
      />
      <aside className="flex w-full max-w-md flex-col border-l border-border bg-panel shadow-2xl">
        <header className="flex items-center justify-between border-b border-border px-5 py-3">
          <h2 className="truncate text-sm font-semibold text-fg">{title}</h2>
          <button
            type="button"
            onClick={onClose}
            aria-label="Close panel"
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
  return (
    <div className="space-y-5">
      <div className="grid grid-cols-2 gap-3 text-sm">
        <DetailRow label="Type" value={<span className="text-fg-muted">{monitor.type}</span>} />
        <DetailRow
          label="Status"
          value={<StatusPill status={monitor.last_status ?? "unknown"}>{monitor.last_status ?? "?"}</StatusPill>}
        />
        <DetailRow label="Interval" value={<span className="tabular-nums">{monitor.interval_sec}s</span>} />
        <DetailRow
          label="Latency"
          value={
            <span className="tabular-nums text-fg-muted">
              {monitor.last_latency_ms ? `${monitor.last_latency_ms} ms` : "—"}
            </span>
          }
        />
        <DetailRow
          label="Target"
          value={<span className="break-all font-mono text-xs text-fg-muted">{monitor.target}</span>}
          full
        />
        {monitor.last_detail && (
          <DetailRow
            label="Last detail"
            value={<span className="break-all font-mono text-xs text-fg-subtle">{monitor.last_detail}</span>}
            full
          />
        )}
      </div>

      <ResultsBlock monitor={monitor} />

      <div>
        <h3 className="mb-2 text-[11px] font-semibold uppercase tracking-wider text-fg-subtle">
          Edit monitor
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
  const tagsQuery = useQuery({
    queryKey: ["tags"],
    queryFn: () => api<{ tags: Array<{ tag: string; count: number }> }>("/v1/tags"),
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
    onError: (err) => setError(err instanceof ApiError ? err.detail : "failed"),
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
        <Field label="Type">
          <select
            value={type}
            disabled={!!initial}
            onChange={(e) => {
              setType(e.target.value as Monitor["type"]);
              setParams({});
            }}
            className="w-full rounded-md border border-border bg-panel px-3 py-2 text-sm disabled:opacity-60"
          >
            {TYPES.map((t) => (
              <option key={t} value={t}>
                {t}
              </option>
            ))}
          </select>
        </Field>
        <Field label="Name">
          <TextInput required value={name} onChange={(e) => setName(e.target.value)} />
        </Field>
      </div>

      <Field label="Target" hint={TARGET_HINT[type]}>
        <TextInput required value={target} onChange={(e) => setTarget(e.target.value)} className="font-mono" />
      </Field>

      {fields.length > 0 && (
        <div className="grid grid-cols-2 gap-3">
          {fields.map((f) => (
            <Field key={f.key} label={f.label}>
              <TextInput
                placeholder={f.placeholder}
                value={params[f.key] ?? ""}
                onChange={(e) => setParams({ ...params, [f.key]: e.target.value })}
              />
            </Field>
          ))}
        </div>
      )}

      <div className="grid grid-cols-2 gap-3">
        <Field label="Interval (seconds)">
          <TextInput
            type="number"
            min={10}
            max={86400}
            value={intervalSec}
            onChange={(e) => setIntervalSec(parseInt(e.target.value || "60", 10))}
          />
        </Field>
        <Field label="Enabled">
          <label className="mt-2 inline-flex items-center gap-2 text-sm text-fg-muted">
            <input type="checkbox" checked={enabled} onChange={(e) => setEnabled(e.target.checked)} />
            On
          </label>
        </Field>
      </div>

      <Field
        label="Apply to tags (comma-separated)"
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
      </Field>

      <Field label="Apply to groups (Ctrl/⌘ to multi-select)">
        <select
          multiple
          size={Math.min(5, Math.max(2, groupsQuery.data?.groups.length ?? 2))}
          value={groupIDs}
          onChange={(e) =>
            setGroupIDs(Array.from(e.target.selectedOptions).map((o) => o.value))
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
          {save.isPending ? "Saving…" : initial ? "Save" : "Create"}
        </Button>
        <Button type="button" onClick={onCancel}>
          Cancel
        </Button>
      </div>
    </form>
  );
}

// ---- Results block (inside slide-over) -----------------------------------

function ResultsBlock({ monitor }: { monitor: Monitor }) {
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
    { label: "latency (ms)", color: colorFor(0), fill: "rgba(16,185,129,0.10)" },
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
          Results
        </h3>
        <TimeRangeSelector value={rangeSec} onChange={setRangeSec} />
      </div>
      {samples.length === 0 ? (
        <Empty>No results in this range.</Empty>
      ) : (
        <>
          <div className="grid grid-cols-4 gap-2 text-xs">
            <SmallStat label="Samples" value={samples.length} />
            <SmallStat label="OK" value={okCount} tone="ok" />
            <SmallStat label="Warn" value={warnCount} tone="warn" />
            <SmallStat label="Fail" value={failCount} tone="fail" />
          </div>
          <p className="mt-2 text-xs text-fg-subtle tabular-nums">
            Effective uptime in window: {uptimePct.toFixed(1)} %
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
