import { keepPreviousData, useQuery } from "@tanstack/react-query";
import { Search } from "lucide-react";
import { useEffect, useMemo, useState } from "react";
import { Link } from "react-router-dom";

import {
  Empty,
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
} from "../components/ui";
import { api } from "../lib/api";
import { Host, ServerLogEntry } from "../lib/types";
import { hostDisplay } from "../lib/utils";

const PAGE_SIZE = 100;
const LEVELS = ["", "debug", "info", "warn", "error"] as const;

type Resp = {
  total: number;
  limit: number;
  offset: number;
  entries: ServerLogEntry[];
  seq: number;
};

export function AdminLogs() {
  const [q, setQ] = useState("");
  const [debounced, setDebounced] = useState("");
  const [level, setLevel] = useState<(typeof LEVELS)[number]>("");
  const [hostID, setHostID] = useState<string>("");
  const [offset, setOffset] = useState(0);
  const [autoRefresh, setAutoRefresh] = useState(true);

  useEffect(() => {
    const t = setTimeout(() => setDebounced(q.trim()), 250);
    return () => clearTimeout(t);
  }, [q]);
  useEffect(() => setOffset(0), [debounced, level, hostID]);

  const hosts = useQuery({
    queryKey: ["hosts-for-filter"],
    queryFn: () => api<{ hosts: Host[] }>("/v1/hosts"),
  });
  const hostMap = useMemo(() => {
    const m = new Map<string, string>();
    for (const h of hosts.data?.hosts ?? []) m.set(h.id, hostDisplay(h));
    return m;
  }, [hosts.data]);

  const params = useMemo(() => {
    const u = new URLSearchParams();
    if (debounced) u.set("q", debounced);
    if (level) u.set("level", level);
    if (hostID) u.set("host_id", hostID);
    u.set("limit", String(PAGE_SIZE));
    u.set("offset", String(offset));
    return u.toString();
  }, [debounced, level, hostID, offset]);

  const logs = useQuery({
    queryKey: ["admin-logs", params],
    queryFn: () => api<Resp>(`/v1/admin/logs?${params}`),
    placeholderData: keepPreviousData,
    refetchInterval: autoRefresh ? 5_000 : false,
  });

  const total = logs.data?.total ?? 0;
  const entries = logs.data?.entries ?? [];

  return (
    <div className="mx-auto max-w-7xl space-y-4 p-6">
      <header className="flex items-center justify-between">
        <div>
          <h2 className="text-lg font-semibold tracking-tight">Server logs</h2>
          <p className="text-sm text-fg-muted">
            In-memory ring buffer. Older entries roll off — ship the JSON
            stream off-host for retention.
          </p>
        </div>
        <p className="text-xs text-fg-subtle tabular-nums">
          {total} matching · seq {logs.data?.seq ?? 0}
        </p>
      </header>

      <Panel>
        <PanelHeader>
          <div className="flex w-full flex-wrap items-center gap-2">
            <div className="relative flex-1 min-w-[240px]">
              <Search className="pointer-events-none absolute left-2.5 top-1/2 h-4 w-4 -translate-y-1/2 text-fg-subtle" />
              <TextInput
                type="search"
                placeholder="search msg / attrs… (case-insensitive)"
                value={q}
                onChange={(e) => setQ(e.target.value)}
                className="pl-8 font-mono"
                autoFocus
              />
            </div>
            <select
              value={level}
              onChange={(e) => setLevel(e.target.value as (typeof LEVELS)[number])}
              className="rounded-md border border-border bg-panel px-3 py-2 text-sm focus:border-accent focus:outline-none"
            >
              {LEVELS.map((l) => (
                <option key={l} value={l}>{l === "" ? "Any level" : l}</option>
              ))}
            </select>
            <select
              value={hostID}
              onChange={(e) => setHostID(e.target.value)}
              className="max-w-[220px] truncate rounded-md border border-border bg-panel px-3 py-2 text-sm focus:border-accent focus:outline-none"
            >
              <option value="">All hosts</option>
              {(hosts.data?.hosts ?? []).map((h) => (
                <option key={h.id} value={h.id}>
                  {hostDisplay(h)}
                </option>
              ))}
            </select>
            <label className="inline-flex items-center gap-2 text-xs text-fg-muted">
              <input
                type="checkbox"
                checked={autoRefresh}
                onChange={(e) => setAutoRefresh(e.target.checked)}
              />
              auto-refresh 5s
            </label>
          </div>
        </PanelHeader>
        <PanelBody className="p-0 overflow-x-auto">
          {logs.isLoading ? (
            <p className="px-5 py-4 text-sm text-fg-subtle">Loading…</p>
          ) : entries.length === 0 ? (
            <Empty>No log entries match.</Empty>
          ) : (
            <Table>
              <THead>
                <tr>
                  <TH>Time</TH>
                  <TH>Level</TH>
                  <TH>Msg</TH>
                  <TH>Attrs</TH>
                </tr>
              </THead>
              <TBody>
                {entries.map((e, i) => (
                  <tr key={`${e.time}-${i}`} className="hover:bg-panel-2 align-top">
                    <TD className="font-mono text-[11px] text-fg-muted whitespace-nowrap">
                      {new Date(e.time).toLocaleTimeString()}
                      <span className="ml-2 text-fg-subtle">
                        {new Date(e.time).toLocaleDateString()}
                      </span>
                    </TD>
                    <TD>
                      <StatusPill status={statusFor(e.level)}>{e.level}</StatusPill>
                    </TD>
                    <TD className="font-mono text-xs">{e.msg}</TD>
                    <TD className="font-mono text-[11px]">
                      <AttrsDisplay attrs={e.attrs} hostMap={hostMap} />
                    </TD>
                  </tr>
                ))}
              </TBody>
            </Table>
          )}
        </PanelBody>
        {total > PAGE_SIZE && (
          <div className="flex items-center justify-between border-t border-border px-5 py-3 text-xs text-fg-muted">
            <span className="tabular-nums">
              {offset + 1}–{Math.min(offset + entries.length, total)} of {total}
            </span>
            <div className="flex items-center gap-2">
              <button
                disabled={offset === 0}
                onClick={() => setOffset(Math.max(0, offset - PAGE_SIZE))}
                className="rounded-md border border-border px-2 py-1 hover:bg-panel-2 disabled:opacity-40"
              >
                Prev
              </button>
              <button
                disabled={offset + PAGE_SIZE >= total}
                onClick={() => setOffset(offset + PAGE_SIZE)}
                className="rounded-md border border-border px-2 py-1 hover:bg-panel-2 disabled:opacity-40"
              >
                Next
              </button>
            </div>
          </div>
        )}
      </Panel>
    </div>
  );
}

function statusFor(level: ServerLogEntry["level"]): "ok" | "warn" | "fail" | "info" {
  switch (level) {
    case "ERROR":
      return "fail";
    case "WARN":
      return "warn";
    case "DEBUG":
      return "info";
    default:
      return "ok";
  }
}

// AttrsDisplay renders a compact key=value list. host_id is rewritten as a
// link to /hosts/{id} (with hostname text when known); other UUIDs and IPs
// stay plain. Long string values truncate with title="full value" tooltip.
function AttrsDisplay({
  attrs,
  hostMap,
}: {
  attrs?: Record<string, unknown>;
  hostMap: Map<string, string>;
}) {
  if (!attrs || Object.keys(attrs).length === 0) {
    return <span className="text-fg-subtle">—</span>;
  }
  return (
    <div className="flex flex-wrap items-baseline gap-x-2 gap-y-0.5">
      {Object.entries(attrs).map(([k, v]) => (
        <span key={k}>
          <span className="text-fg-subtle">{k}=</span>
          {renderValue(k, v, hostMap)}
        </span>
      ))}
    </div>
  );
}

function renderValue(key: string, value: unknown, hostMap: Map<string, string>) {
  const s = typeof value === "string" ? value : JSON.stringify(value);
  if (key === "host_id" && typeof value === "string") {
    const name = hostMap.get(value);
    return (
      <Link to={`/hosts/${value}`} className="text-accent hover:underline" title={value}>
        {name ?? value.substring(0, 8)}
      </Link>
    );
  }
  if (key === "hostname" && typeof value === "string") {
    return <span className="text-fg">{value}</span>;
  }
  if (s.length > 80) {
    return (
      <span title={s} className="text-fg-muted">
        {s.substring(0, 77)}…
      </span>
    );
  }
  return <span className="text-fg-muted">{s}</span>;
}
