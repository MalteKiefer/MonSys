import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { Activity, PencilLine, Plus, Trash2 } from "lucide-react";
import { FormEvent, useMemo, useState } from "react";

import { ChartLine, ChartSeries, colorFor } from "../components/Chart";
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

export function AdminMonitors() {
  const qc = useQueryClient();
  const list = useQuery({
    queryKey: ["monitors"],
    queryFn: () => api<{ monitors: Monitor[] }>("/v1/monitors"),
    refetchInterval: 30_000,
  });

  const [editing, setEditing] = useState<Monitor | null>(null);
  const [creating, setCreating] = useState(false);
  const [showHistory, setShowHistory] = useState<Monitor | null>(null);

  return (
    <div className="mx-auto max-w-6xl space-y-5 p-6">
      <header className="flex items-center justify-between">
        <div>
          <h2 className="text-lg font-semibold tracking-tight">Monitors</h2>
          <p className="text-sm text-fg-muted">
            Server-side periodic probes: cert expiry, DB reachability, HTTP, raw TCP.
          </p>
        </div>
        <Button variant="primary" onClick={() => setCreating(true)}>
          <Plus className="h-3.5 w-3.5" /> New monitor
        </Button>
      </header>

      {(creating || editing) && (
        <MonitorForm
          initial={editing}
          onCancel={() => {
            setEditing(null);
            setCreating(false);
          }}
          onSaved={() => {
            qc.invalidateQueries({ queryKey: ["monitors"] });
            setEditing(null);
            setCreating(false);
          }}
        />
      )}

      {showHistory && (
        <ResultsPanel
          monitor={showHistory}
          onClose={() => setShowHistory(null)}
        />
      )}

      <Panel>
        <PanelHeader>
          <h3 className="text-sm font-semibold">All monitors</h3>
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
                  <tr key={m.id} className="hover:bg-panel-2">
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
                      <div className="inline-flex items-center gap-1">
                        <Button onClick={() => setShowHistory(m)}>
                          <Activity className="h-3.5 w-3.5" /> History
                        </Button>
                        <Button onClick={() => setEditing(m)}>
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
    </div>
  );
}

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
    <Panel>
      <PanelHeader>
        <h3 className="text-sm font-semibold">{initial ? `Edit ${initial.name}` : "New monitor"}</h3>
      </PanelHeader>
      <PanelBody>
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
      </PanelBody>
    </Panel>
  );
}

function ResultsPanel({ monitor, onClose }: { monitor: Monitor; onClose: () => void }) {
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
    <Panel>
      <PanelHeader>
        <div className="flex items-center gap-3">
          <Activity className="h-4 w-4 text-fg-muted" />
          <h3 className="text-sm font-semibold">
            {monitor.type} · {monitor.name} — results
          </h3>
        </div>
        <div className="flex items-center gap-2">
          <TimeRangeSelector value={rangeSec} onChange={setRangeSec} />
          <Button onClick={onClose}>Close</Button>
        </div>
      </PanelHeader>
      <PanelBody>
        {samples.length === 0 ? (
          <Empty>No results in this range.</Empty>
        ) : (
          <>
            <div className="grid grid-cols-2 gap-3 md:grid-cols-4">
              <SmallStat label="Samples" value={samples.length} />
              <SmallStat label="OK" value={okCount} tone="ok" />
              <SmallStat label="Warn" value={warnCount} tone="warn" />
              <SmallStat label="Fail" value={failCount} tone="fail" />
            </div>
            <p className="mt-3 text-xs text-fg-subtle tabular-nums">
              Effective uptime in window: {uptimePct.toFixed(1)} %
            </p>
            <div className="mt-4">
              <ChartLine data={{ matrix }} series={series} formatY={(v) => `${v.toFixed(0)} ms`} yMin={0} height={200} />
            </div>
          </>
        )}
      </PanelBody>
    </Panel>
  );
}

function SmallStat({ label, value, tone }: { label: string; value: number; tone?: "ok" | "warn" | "fail" }) {
  const color = tone === "ok" ? "text-ok" : tone === "warn" ? "text-warn" : tone === "fail" ? "text-fail" : "text-fg";
  return (
    <div className="rounded-lg border border-border bg-panel-2 p-3">
      <p className="text-[11px] uppercase tracking-wider text-fg-subtle">{label}</p>
      <p className={`mt-0.5 text-xl font-semibold tabular-nums ${color}`}>{value}</p>
    </div>
  );
}

