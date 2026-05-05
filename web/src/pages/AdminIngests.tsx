import { useQuery } from "@tanstack/react-query";
import { ChevronDown, ChevronRight, Copy, FileJson, RefreshCcw } from "lucide-react";
import { useMemo, useState } from "react";
import { Link } from "react-router-dom";

import { Page } from "../components/page";
import {
  Button,
  Empty,
  Panel,
  PanelBody,
  PanelHeader,
  Skeleton,
  TBody,
  TD,
  TH,
  THead,
  Table,
  TimeRangeSelector,
} from "../components/ui";
import { api } from "../lib/api";
import { Host, IngestPayload, IngestSummary } from "../lib/types";
import { hostDisplay } from "../lib/utils";

// Time-range cull options for the recent-ingests list. The ring buffer is
// already capped at ~100 items so this is a client-side filter — no extra
// network calls. "all" (seconds = 0) means "no cutoff".
const RANGE_OPTIONS = [
  { label: "15m", seconds: 15 * 60 },
  { label: "1h", seconds: 60 * 60 },
  { label: "6h", seconds: 6 * 60 * 60 },
  { label: "24h", seconds: 24 * 60 * 60 },
  { label: "all", seconds: 0 },
];

// Thresholds at which the tree view flags an array/string as oversized.
// Tuned for the agent's typical payload — anything past these bounds is
// worth surfacing visually.
const LARGE_ARRAY_THRESHOLD = 50;
const LARGE_STRING_THRESHOLD = 512;
const ARRAY_PAGE = 5;

export function AdminIngests() {
  const [hostID, setHostID] = useState("");
  const [selectedIdx, setSelectedIdx] = useState<number | null>(null);
  const [rangeSec, setRangeSec] = useState<number>(0);

  const hosts = useQuery({
    queryKey: ["hosts-for-filter"],
    queryFn: () => api<{ hosts: Host[] }>("/v1/hosts"),
  });

  const params = useMemo(() => {
    const u = new URLSearchParams();
    if (hostID) u.set("host_id", hostID);
    u.set("limit", "100");
    return u.toString();
  }, [hostID]);

  const list = useQuery({
    queryKey: ["admin-ingests", params],
    queryFn: () => api<{ entries: IngestSummary[] }>(`/v1/admin/ingests?${params}`),
    refetchInterval: 5_000,
  });

  const detail = useQuery({
    queryKey: ["admin-ingest", selectedIdx, hostID],
    queryFn: () => {
      const u = new URLSearchParams();
      if (hostID) u.set("host_id", hostID);
      return api<IngestPayload>(`/v1/admin/ingests/${selectedIdx}?${u.toString()}`);
    },
    enabled: selectedIdx !== null,
  });

  // Cull the list client-side by the chosen time window.
  const visibleEntries = useMemo(() => {
    const all = list.data?.entries ?? [];
    if (!rangeSec) return all;
    const cutoff = Date.now() - rangeSec * 1000;
    return all.filter((e) => {
      const t = new Date(e.time).getTime();
      return Number.isFinite(t) && t >= cutoff;
    });
  }, [list.data, rangeSec]);

  return (
    <Page
      title="Agent ingests"
      subtitle={`Last ${list.data?.entries.length ?? 0} captured payloads. Re-marshalled JSON; semantically identical to what the agent sent.`}
      breadcrumb={[{ label: "Admin" }, { label: "Agent ingests" }]}
      actions={
        <div className="flex items-center gap-2">
          <TimeRangeSelector value={rangeSec} onChange={setRangeSec} options={RANGE_OPTIONS} />
          <select
            value={hostID}
            onChange={(e) => {
              setHostID(e.target.value);
              setSelectedIdx(null);
            }}
            className="rounded-md border border-border bg-panel px-3 py-2 text-sm focus:border-accent focus:outline-none"
          >
            <option value="">All hosts</option>
            {(hosts.data?.hosts ?? []).map((h) => (
              <option key={h.id} value={h.id}>{hostDisplay(h)}</option>
            ))}
          </select>
        </div>
      }
    >
      <div className="grid gap-4 lg:grid-cols-[420px_1fr]">
        <Panel>
          <PanelHeader>
            <h3 className="text-sm font-semibold">Recent</h3>
            <Button onClick={() => list.refetch()}>
              <RefreshCcw className="h-3.5 w-3.5" /> Refresh
            </Button>
          </PanelHeader>
          <PanelBody className="p-0 overflow-x-auto max-h-[70vh]">
            {list.isLoading ? (
              <div className="p-5">
                <Skeleton className="h-48" />
              </div>
            ) : visibleEntries.length === 0 ? (
              <Empty>No ingests captured yet.</Empty>
            ) : (
              <Table>
                <THead>
                  <tr>
                    <TH>When</TH>
                    <TH>Host</TH>
                    <TH>Size</TH>
                  </tr>
                </THead>
                <TBody>
                  {visibleEntries.map((e) => {
                    const active = selectedIdx === e.idx;
                    return (
                      <tr
                        key={`${e.host_id}-${e.idx}-${e.time}`}
                        onClick={() => setSelectedIdx(e.idx)}
                        className={`cursor-pointer ${active ? "bg-panel-2" : "hover:bg-panel-2"}`}
                      >
                        <TD className="font-mono text-[11px] text-fg-muted">{relTime(e.time)}</TD>
                        <TD>
                          <Link
                            to={`/hosts/${e.host_id}`}
                            onClick={(ev) => ev.stopPropagation()}
                            className="text-accent hover:underline"
                          >
                            {e.hostname || e.host_id.substring(0, 8)}
                          </Link>
                        </TD>
                        <TD className="tabular-nums text-fg-muted whitespace-nowrap">
                          {formatBytes(e.size_bytes)}
                        </TD>
                      </tr>
                    );
                  })}
                </TBody>
              </Table>
            )}
          </PanelBody>
        </Panel>

        <Panel>
          <PanelHeader>
            <div className="flex items-center gap-2">
              <FileJson className="h-4 w-4 text-fg-muted" />
              <h3 className="text-sm font-semibold">
                {selectedIdx === null ? "Pick an ingest" : "Payload"}
              </h3>
              {detail.data?.truncated && (
                <span className="rounded-md bg-warn/10 px-1.5 py-0.5 text-[10px] text-warn ring-1 ring-inset ring-warn/30">
                  truncated
                </span>
              )}
            </div>
            {detail.data && (
              <Button
                onClick={() => {
                  void navigator.clipboard.writeText(JSON.stringify(detail.data!.payload, null, 2));
                }}
              >
                <Copy className="h-3.5 w-3.5" /> Copy
              </Button>
            )}
          </PanelHeader>
          <PanelBody className="max-h-[70vh] overflow-auto p-0">
            {selectedIdx === null ? (
              <p className="px-5 py-8 text-center text-sm text-fg-subtle">
                Click a row to inspect the JSON the agent sent.
              </p>
            ) : detail.isLoading ? (
              <div className="p-5">
                <Skeleton className="h-64" />
              </div>
            ) : detail.error ? (
              <p className="px-5 py-4 text-sm text-fail">{(detail.error as Error).message}</p>
            ) : (
              <div className="px-5 py-4 font-mono text-[11px] leading-relaxed text-fg">
                <TreeNode value={detail.data?.payload} k="root" depth={0} initialOpen />
              </div>
            )}
          </PanelBody>
        </Panel>
      </div>
    </Page>
  );
}

// ---- Tree view -----------------------------------------------------------
//
// A small recursive renderer. Each object/array node owns its own collapsed
// state; arrays paginate at ARRAY_PAGE items with "show more" / "show all"
// affordances so a 10k-element array doesn't render at once. Large arrays
// and strings get a yellow chip so reviewers spot oversized chunks at a
// glance. No external library — pure recursion.

type TreeNodeProps = {
  value: unknown;
  k: string | number;
  depth: number;
  initialOpen?: boolean;
};

function TreeNode({ value, k, depth, initialOpen = false }: TreeNodeProps) {
  // Top-level always opens; nested defaults to closed unless caller
  // overrides. Each node owns its own state so toggling one branch never
  // disturbs siblings.
  const [open, setOpen] = useState<boolean>(initialOpen || depth === 0);

  if (value === null) return <Leaf k={k}><span className="text-fg-subtle">null</span></Leaf>;
  if (value === undefined) return <Leaf k={k}><span className="text-fg-subtle">undefined</span></Leaf>;

  if (Array.isArray(value)) {
    return (
      <ArrayNode
        k={k}
        arr={value}
        depth={depth}
        open={open}
        onToggle={() => setOpen((o) => !o)}
      />
    );
  }

  if (typeof value === "object") {
    const obj = value as Record<string, unknown>;
    const entries = Object.entries(obj);
    return (
      <div>
        <button
          type="button"
          onClick={() => setOpen((o) => !o)}
          className="inline-flex items-center gap-1 rounded text-fg-muted hover:text-fg"
        >
          {open ? (
            <ChevronDown className="h-3 w-3 text-fg-subtle" />
          ) : (
            <ChevronRight className="h-3 w-3 text-fg-subtle" />
          )}
          <KeyLabel k={k} />
          <span className="text-fg-subtle">
            {open ? "{" : `{ ${entries.length} ${entries.length === 1 ? "key" : "keys"} }`}
          </span>
        </button>
        {open && (
          <div className="ml-4 border-l border-border pl-3">
            {entries.map(([ck, cv]) => (
              <TreeNode key={ck} k={ck} value={cv} depth={depth + 1} />
            ))}
            <div className="text-fg-subtle">{"}"}</div>
          </div>
        )}
      </div>
    );
  }

  return <Leaf k={k}>{renderPrimitive(value)}</Leaf>;
}

function ArrayNode({
  k,
  arr,
  depth,
  open,
  onToggle,
}: {
  k: string | number;
  arr: unknown[];
  depth: number;
  open: boolean;
  onToggle: () => void;
}) {
  const [shown, setShown] = useState<number>(ARRAY_PAGE);
  const isLarge = arr.length >= LARGE_ARRAY_THRESHOLD;
  const visible = arr.slice(0, shown);
  const remaining = arr.length - shown;

  return (
    <div>
      <button
        type="button"
        onClick={onToggle}
        className="inline-flex items-center gap-1 rounded text-fg-muted hover:text-fg"
      >
        {open ? (
          <ChevronDown className="h-3 w-3 text-fg-subtle" />
        ) : (
          <ChevronRight className="h-3 w-3 text-fg-subtle" />
        )}
        <KeyLabel k={k} />
        <span className="text-fg-subtle">
          {open ? "[" : `[ ${arr.length} ${arr.length === 1 ? "item" : "items"} ]`}
        </span>
        {isLarge && (
          <span className="ml-1 rounded bg-warn/10 px-1 py-0 text-[9px] font-medium text-warn ring-1 ring-inset ring-warn/30">
            large
          </span>
        )}
      </button>
      {open && (
        <div className="ml-4 border-l border-border pl-3">
          {visible.map((v, i) => (
            <TreeNode key={i} k={i} value={v} depth={depth + 1} />
          ))}
          {remaining > 0 && (
            <div className="mt-0.5 flex items-center gap-2">
              <button
                type="button"
                onClick={() => setShown((s) => s + ARRAY_PAGE)}
                className="rounded-md border border-border bg-panel px-2 py-0.5 text-[10px] text-fg-muted hover:bg-panel-2 hover:text-fg"
              >
                show more (+{Math.min(ARRAY_PAGE, remaining)})
              </button>
              {remaining > ARRAY_PAGE && (
                <button
                  type="button"
                  onClick={() => setShown(arr.length)}
                  className="rounded-md border border-border bg-panel px-2 py-0.5 text-[10px] text-fg-muted hover:bg-panel-2 hover:text-fg"
                >
                  show all ({remaining})
                </button>
              )}
              <span className="text-[10px] text-fg-subtle tabular-nums">
                {shown}/{arr.length}
              </span>
            </div>
          )}
          <div className="text-fg-subtle">]</div>
        </div>
      )}
    </div>
  );
}

function Leaf({ k, children }: { k: string | number; children: React.ReactNode }) {
  return (
    <div className="flex items-baseline gap-1">
      <KeyLabel k={k} />
      {children}
    </div>
  );
}

function KeyLabel({ k }: { k: string | number }) {
  if (k === "root") return null;
  return (
    <span className="text-fg-subtle">
      {typeof k === "number" ? `[${k}]` : `${k}:`}
    </span>
  );
}

function renderPrimitive(v: unknown) {
  if (typeof v === "string") {
    const big = v.length > LARGE_STRING_THRESHOLD;
    const display = big ? `${v.slice(0, 120)}… (${v.length} chars)` : v;
    return (
      <span className={big ? "text-warn" : "text-ok"} title={big ? v : undefined}>
        "{display}"
        {big && (
          <span className="ml-1 rounded bg-warn/10 px-1 py-0 text-[9px] font-medium text-warn ring-1 ring-inset ring-warn/30">
            big
          </span>
        )}
      </span>
    );
  }
  if (typeof v === "number") return <span className="text-info tabular-nums">{v}</span>;
  if (typeof v === "boolean") return <span className="text-info">{String(v)}</span>;
  return <span className="text-fg-muted">{String(v)}</span>;
}

function relTime(iso: string): string {
  const t = new Date(iso).getTime();
  if (Number.isNaN(t)) return iso;
  const diff = (Date.now() - t) / 1000;
  if (diff < 60) return `${Math.round(diff)}s ago`;
  if (diff < 3600) return `${Math.round(diff / 60)}m ago`;
  if (diff < 86400) return `${Math.round(diff / 3600)}h ago`;
  return `${Math.round(diff / 86400)}d ago`;
}

function formatBytes(n: number): string {
  if (!n) return "0";
  const u = ["B", "KiB", "MiB", "GiB"];
  let v = n;
  let i = 0;
  while (v >= 1024 && i < u.length - 1) {
    v /= 1024;
    i++;
  }
  return `${v.toFixed(v < 10 && i > 0 ? 1 : 0)} ${u[i]}`;
}
