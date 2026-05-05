import { useQuery } from "@tanstack/react-query";
import { Copy, FileJson, RefreshCcw } from "lucide-react";
import { useMemo, useState } from "react";
import { Link } from "react-router-dom";

import {
  Button,
  Empty,
  Panel,
  PanelBody,
  PanelHeader,
  TBody,
  TD,
  TH,
  THead,
  Table,
} from "../components/ui";
import { api } from "../lib/api";
import { Host, IngestPayload, IngestSummary } from "../lib/types";
import { hostDisplay } from "../lib/utils";

export function AdminIngests() {
  const [hostID, setHostID] = useState("");
  const [selectedIdx, setSelectedIdx] = useState<number | null>(null);

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

  return (
    <div className="mx-auto max-w-7xl space-y-4 p-6">
      <header className="flex items-center justify-between">
        <div>
          <h2 className="text-lg font-semibold tracking-tight">Agent ingests</h2>
          <p className="text-sm text-fg-muted">
            Last {list.data?.entries.length ?? 0} captured payloads. Re-marshalled
            JSON; semantically identical to what the agent sent.
          </p>
        </div>
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
      </header>

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
              <p className="px-5 py-4 text-sm text-fg-subtle">Loading…</p>
            ) : (list.data?.entries ?? []).length === 0 ? (
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
                  {(list.data?.entries ?? []).map((e) => {
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
                            {/* TODO: pass labels for display */}
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
              <p className="px-5 py-4 text-sm text-fg-subtle">Loading…</p>
            ) : detail.error ? (
              <p className="px-5 py-4 text-sm text-fail">{(detail.error as Error).message}</p>
            ) : (
              <pre className="m-0 px-5 py-4 font-mono text-[11px] leading-relaxed text-fg whitespace-pre-wrap break-words">
                {JSON.stringify(detail.data?.payload, null, 2)}
              </pre>
            )}
          </PanelBody>
        </Panel>
      </div>
    </div>
  );
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
