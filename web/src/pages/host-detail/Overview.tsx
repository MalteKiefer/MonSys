import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { Cpu, Tag, X } from "lucide-react";
import { KeyboardEvent, useMemo, useState } from "react";

import {
  ChartLine,
  ChartSeries,
  colorFor,
  formatBytes,
  formatPercent,
} from "../../components/Chart";
import {
  Empty,
  Panel,
  PanelBody,
  PanelHeader,
  Skeleton,
  StatCard,
  TimeRangeSelector,
} from "../../components/ui";
import { api } from "../../lib/api";
import { SystemSample } from "../../lib/types";

import { useHostDetail } from "./HostLayout";

// Overview is the default landing pane for /hosts/:id. It mirrors the legacy
// "overview" tab from the monolithic page: a small live-system chart plus the
// inline-editable tag/group/label metadata. Tag editing stays inline (rather
// than living behind the kebab menu) because that's both the simpler call-
// site change and the lower-friction interaction for operators.
export function Overview() {
  const { detail, hostId } = useHostDetail();
  const [rangeSec, setRangeSec] = useState(60 * 60); // 1h default

  const fromTo = useMemo(() => {
    const to = new Date();
    const from = new Date(to.getTime() - rangeSec * 1000);
    return { from: from.toISOString(), to: to.toISOString() };
  }, [rangeSec]);
  const queryStr = `from=${encodeURIComponent(fromTo.from)}&to=${encodeURIComponent(fromTo.to)}`;

  const sys = useQuery({
    queryKey: ["host-system", hostId, rangeSec],
    queryFn: () => api<{ samples: SystemSample[] }>(`/v1/hosts/${hostId}/metrics/system?${queryStr}`),
    refetchInterval: 15_000,
    enabled: !!hostId,
  });

  const h = detail.host;

  return (
    <div className="space-y-5">
      <Panel>
        <div className="flex flex-wrap items-start gap-3 p-4">
          <TagsEditor hostID={h.id} initial={h.tags} />
          {(h.groups ?? []).length > 0 && (
            <div className="flex flex-wrap items-center gap-1">
              <span className="text-[11px] font-medium uppercase tracking-wider text-fg-subtle">Groups:</span>
              {(h.groups ?? []).map((g) => (
                <span key={g.id} className="rounded-md bg-info/10 px-2 py-0.5 text-[11px] font-mono text-info ring-1 ring-inset ring-info/30">
                  {g.name}
                </span>
              ))}
            </div>
          )}
          {Object.keys(h.labels).length > 0 && (
            <div className="ml-auto flex flex-wrap gap-1.5">
              {Object.entries(h.labels).map(([k, v]) => (
                <span key={k} className="rounded-md bg-panel-2 px-2 py-0.5 text-[11px] font-mono text-fg-muted">
                  {k}={v}
                </span>
              ))}
            </div>
          )}
        </div>
      </Panel>

      <div className="grid grid-cols-2 gap-3 md:grid-cols-4">
        <StatCard label="CPU cores" value={h.cpu_cores} />
        <StatCard label="RAM" value={formatBytes(h.ram_total_bytes)} />
        <StatCard label="First seen" value={new Date(h.first_seen_at).toLocaleDateString()} />
        <StatCard label="Status since" value={h.status_since ? relativeTime(h.status_since) : "—"} />
      </div>

      <div className="flex items-center justify-between">
        <h2 className="text-[11px] font-semibold uppercase tracking-wider text-fg-subtle">Live system</h2>
        <TimeRangeSelector value={rangeSec} onChange={setRangeSec} />
      </div>
      <SystemPanel
        samples={sys.data?.samples ?? []}
        ramTotal={h.ram_total_bytes}
        loading={sys.isLoading}
      />
    </div>
  );
}

// ---- Inline tags editor (lifted from HostDetail.tsx, unchanged) ---------

function TagsEditor({ hostID, initial }: { hostID: string; initial: string[] }) {
  const qc = useQueryClient();
  const [tags, setTags] = useState<string[]>(initial);
  const [draft, setDraft] = useState("");
  const [editing, setEditing] = useState(false);

  const save = useMutation({
    mutationFn: (next: string[]) =>
      api(`/v1/hosts/${hostID}/tags`, { method: "PUT", body: JSON.stringify({ tags: next }) }),
    onSuccess: () => qc.invalidateQueries({ queryKey: ["host", hostID] }),
  });

  function commit(next: string[]) {
    setTags(next);
    save.mutate(next);
  }
  function add() {
    const t = draft.trim().toLowerCase();
    if (!t) return;
    if (tags.includes(t)) {
      setDraft("");
      return;
    }
    commit([...tags, t]);
    setDraft("");
  }
  function onKey(e: KeyboardEvent<HTMLInputElement>) {
    if (e.key === "Enter" || e.key === ",") {
      e.preventDefault();
      add();
    } else if (e.key === "Escape") {
      setDraft("");
      setEditing(false);
    }
  }

  return (
    <div className="flex flex-wrap items-center gap-1">
      <span className="text-[11px] font-medium uppercase tracking-wider text-fg-subtle">
        <Tag className="mr-1 inline h-3 w-3" /> Tags:
      </span>
      {tags.map((t) => (
        <span key={t} className="inline-flex items-center gap-0.5 rounded-md bg-panel-2 pl-1.5 pr-0.5 py-0.5 font-mono text-[10px] text-accent">
          #{t}
          <button onClick={() => commit(tags.filter((x) => x !== t))} className="rounded p-0.5 text-fg-subtle hover:bg-fail/20 hover:text-fail" aria-label={`Remove tag ${t}`}>
            <X className="h-3 w-3" />
          </button>
        </span>
      ))}
      {editing ? (
        <input
          autoFocus
          value={draft}
          onBlur={() => {
            if (draft) add();
            setEditing(false);
          }}
          onChange={(e) => setDraft(e.target.value)}
          onKeyDown={onKey}
          placeholder="add tag…"
          className="w-24 rounded-md border border-border bg-panel px-1.5 py-0.5 font-mono text-[10px] focus:border-accent focus:outline-none"
        />
      ) : (
        <button onClick={() => setEditing(true)} className="rounded-md border border-dashed border-border px-2 py-0.5 text-[10px] text-fg-subtle hover:text-fg hover:border-border-strong">
          + add tag
        </button>
      )}
      {save.isError && <span className="text-[10px] text-fail">save failed</span>}
    </div>
  );
}

// ---- Live system panel (CPU / RAM / load) ------------------------------

function SystemPanel({ samples, ramTotal, loading }: { samples: SystemSample[]; ramTotal: number; loading: boolean }) {
  const latest = samples.at(-1);
  const ramPct = latest && ramTotal > 0 ? (latest.ram_used_bytes / ramTotal) * 100 : 0;

  const matrix = useMemo(() => {
    const t = samples.map((s) => Math.floor(new Date(s.time).getTime() / 1000));
    const cpu = samples.map((s) => s.cpu_usage_pct);
    const ram = samples.map((s) => (ramTotal > 0 ? (s.ram_used_bytes / ramTotal) * 100 : 0));
    return [t, cpu, ram];
  }, [samples, ramTotal]);

  const series: ChartSeries[] = [
    { label: "CPU", color: colorFor(0), fill: "rgba(16,185,129,0.10)" },
    { label: "RAM", color: colorFor(1), fill: "rgba(96,165,250,0.10)" },
  ];

  return (
    <Panel>
      <PanelHeader>
        <div className="flex items-center gap-2">
          <Cpu className="h-4 w-4 text-fg-muted" />
          <h3 className="text-sm font-semibold">System</h3>
        </div>
      </PanelHeader>
      <PanelBody>
        {loading && samples.length === 0 ? (
          <Skeleton className="h-48" />
        ) : !latest ? (
          <Empty>No system samples in this range.</Empty>
        ) : (
          <>
            <div className="grid grid-cols-2 gap-3 md:grid-cols-5">
              <StatCard label="CPU" value={`${latest.cpu_usage_pct.toFixed(1)}%`} hint={relativeTime(latest.time)} />
              <StatCard label="RAM" value={`${ramPct.toFixed(0)}%`} hint={formatBytes(latest.ram_used_bytes)} />
              <StatCard label="Load 1/5/15" value={`${latest.load_1.toFixed(2)} / ${latest.load_5.toFixed(2)} / ${latest.load_15.toFixed(2)}`} />
              <StatCard label="Swap" value={formatBytes(latest.swap_used_bytes)} />
              <StatCard label="Uptime" value={formatUptime(latest.uptime_sec)} />
            </div>
            <div className="mt-4">
              <ChartLine data={{ matrix }} series={series} formatY={formatPercent} yMin={0} />
            </div>
          </>
        )}
      </PanelBody>
    </Panel>
  );
}

function formatUptime(sec: number): string {
  if (sec < 60) return `${sec}s`;
  if (sec < 3600) return `${Math.round(sec / 60)}m`;
  if (sec < 86400) return `${(sec / 3600).toFixed(1)}h`;
  return `${Math.round(sec / 86400)}d`;
}

function relativeTime(iso: string): string {
  const t = new Date(iso).getTime();
  if (Number.isNaN(t)) return iso;
  const diff = (Date.now() - t) / 1000;
  if (diff < 60) return `${Math.round(diff)}s ago`;
  if (diff < 3600) return `${Math.round(diff / 60)}m ago`;
  if (diff < 86400) return `${Math.round(diff / 3600)}h ago`;
  return `${Math.round(diff / 86400)}d ago`;
}
