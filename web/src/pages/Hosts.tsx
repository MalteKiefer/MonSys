import { useQuery } from "@tanstack/react-query";
import { Server } from "lucide-react";
import { useNavigate } from "react-router-dom";

import { DistroIcon } from "../components/icons/DistroIcon";
import { ServiceBadges } from "../components/icons/ServiceIcon";
import { Panel, SectionHeading, StatusPill, TBody, TD, TH, THead, Table } from "../components/ui";
import { api } from "../lib/api";
import { Host } from "../lib/types";

type HostsResponse = { hosts: Host[] };

export function Hosts() {
  const navigate = useNavigate();
  const { data, isLoading, error } = useQuery({
    queryKey: ["hosts"],
    queryFn: () => api<HostsResponse>("/v1/hosts"),
    refetchInterval: 15_000,
  });

  if (isLoading) {
    return <p className="p-6 text-sm text-fg-muted">Loading hosts…</p>;
  }
  if (error) {
    return <p className="p-6 text-sm text-fail">{(error as Error).message}</p>;
  }
  const hosts = data?.hosts ?? [];

  return (
    <div className="mx-auto max-w-7xl space-y-4 p-6">
      <div className="flex items-center justify-between">
        <SectionHeading>Hosts</SectionHeading>
        <p className="text-xs text-fg-subtle tabular-nums">{hosts.length} known</p>
      </div>

      <Panel>
        {hosts.length === 0 ? (
          <div className="flex flex-col items-center gap-3 px-6 py-12 text-center">
            <Server className="h-10 w-10 text-fg-subtle" />
            <p className="text-sm text-fg-muted">No hosts yet.</p>
            <p className="text-xs text-fg-subtle">
              Issue a bootstrap token via <code className="font-mono text-fg-muted">mon-server --new-token</code>
              {" "}then run mon-agent.
            </p>
          </div>
        ) : (
          <Table>
            <THead>
              <tr>
                <TH>Status</TH>
                <TH>Host</TH>
                <TH>Distro</TH>
                <TH>Services</TH>
                <TH>Tags / Groups</TH>
                <TH>CPU / RAM</TH>
                <TH>Last seen</TH>
              </tr>
            </THead>
            <TBody>
              {hosts.map((h) => (
                <tr
                  key={h.id}
                  className="cursor-pointer transition-colors duration-100 hover:bg-panel-2"
                  onClick={() => navigate(`/hosts/${h.id}`)}
                >
                  <TD><StatusPill status={h.status} /></TD>
                  <TD>
                    <span className="font-medium text-fg">{h.hostname}</span>
                    <span className="ml-2 font-mono text-[10px] text-fg-subtle">{h.arch}</span>
                  </TD>
                  <TD>
                    <span className="inline-flex items-center gap-1.5">
                      <DistroIcon family={h.distro_family} />
                      <span className="text-fg-muted">{h.distro || "—"}</span>
                    </span>
                  </TD>
                  <TD>
                    <ServiceBadges services={h.services} />
                    {(!h.services || h.services.length === 0) && (
                      <span className="text-fg-subtle">—</span>
                    )}
                  </TD>
                  <TD>
                    <div className="flex flex-wrap items-center gap-1">
                      {h.tags.map((t) => (
                        <span key={t} className="rounded-md bg-panel-2 px-1.5 py-0.5 font-mono text-[10px] text-accent">
                          #{t}
                        </span>
                      ))}
                      {h.groups.map((g) => (
                        <span
                          key={g.id}
                          className="rounded-md bg-info/10 px-1.5 py-0.5 font-mono text-[10px] text-info ring-1 ring-inset ring-info/30"
                        >
                          {g.name}
                        </span>
                      ))}
                      {h.tags.length === 0 && h.groups.length === 0 && (
                        <span className="text-fg-subtle">—</span>
                      )}
                    </div>
                  </TD>
                  <TD className="tabular-nums text-fg-muted whitespace-nowrap">
                    {h.cpu_cores}c · {formatBytes(h.ram_total_bytes)}
                  </TD>
                  <TD className="text-fg-muted whitespace-nowrap">{relativeTime(h.last_seen_at)}</TD>
                </tr>
              ))}
            </TBody>
          </Table>
        )}
      </Panel>
    </div>
  );
}

function formatBytes(n: number): string {
  if (!n) return "—";
  const units = ["B", "KiB", "MiB", "GiB", "TiB"];
  let v = n;
  let i = 0;
  while (v >= 1024 && i < units.length - 1) {
    v /= 1024;
    i++;
  }
  return `${v.toFixed(v < 10 && i > 0 ? 1 : 0)} ${units[i]}`;
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
