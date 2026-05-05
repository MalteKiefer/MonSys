import { useQuery } from "@tanstack/react-query";
import { Plus, Server } from "lucide-react";
import { KeyboardEvent, useMemo, useState } from "react";
import { useNavigate } from "react-router-dom";

import { EnrollAgentModal } from "../components/EnrollAgentModal";
import { DistroIcon } from "../components/icons/DistroIcon";
import { ServiceBadges } from "../components/icons/ServiceIcon";
import {
  Button,
  Field,
  Panel,
  SectionHeading,
  StatusPill,
  TBody,
  TD,
  TH,
  THead,
  Table,
  TextInput,
} from "../components/ui";
import { api } from "../lib/api";
import { useAuth } from "../lib/auth";
import { Host } from "../lib/types";
import { hostDisplay } from "../lib/utils";

type HostsResponse = { hosts: Host[] };

type StatusFilter = "all" | "online" | "stale" | "offline";

const STATUS_FILTERS: { key: StatusFilter; label: string }[] = [
  { key: "all", label: "All" },
  { key: "online", label: "Online" },
  { key: "stale", label: "Stale" },
  { key: "offline", label: "Offline" },
];

export function Hosts() {
  const navigate = useNavigate();
  const isAdmin = useAuth((s) => s.user?.role === "admin");
  const { data, isLoading, error } = useQuery({
    queryKey: ["hosts"],
    queryFn: () => api<HostsResponse>("/v1/hosts"),
    refetchInterval: 15_000,
  });

  const [search, setSearch] = useState("");
  const [statusFilter, setStatusFilter] = useState<StatusFilter>("all");
  const [enrollOpen, setEnrollOpen] = useState(false);

  const hosts = data?.hosts ?? [];

  const filtered = useMemo(() => {
    const needle = search.trim().toLowerCase();
    return hosts.filter((h) => {
      if (statusFilter !== "all" && h.status !== statusFilter) return false;
      if (needle === "") return true;
      if (hostDisplay(h).toLowerCase().includes(needle)) return true;
      if (h.tags.some((t) => t.toLowerCase().includes(needle))) return true;
      return false;
    });
  }, [hosts, search, statusFilter]);

  if (isLoading) {
    return <p className="p-6 text-sm text-fg-muted">Loading hosts…</p>;
  }
  if (error) {
    return <p className="p-6 text-sm text-fail">{(error as Error).message}</p>;
  }

  const onRowKeyDown = (e: KeyboardEvent<HTMLTableRowElement>, id: string) => {
    if (e.key === "Enter" || e.key === " ") {
      e.preventDefault();
      navigate(`/hosts/${id}`);
    }
  };

  return (
    <div className="mx-auto max-w-7xl space-y-4 p-6">
      <div className="flex items-center justify-between gap-3">
        <SectionHeading>Hosts</SectionHeading>
        <div className="flex items-center gap-3">
          <p className="text-xs text-fg-subtle tabular-nums">
            {filtered.length === hosts.length
              ? `${hosts.length} known`
              : `${filtered.length} of ${hosts.length}`}
          </p>
          {isAdmin && (
            <Button variant="primary" onClick={() => setEnrollOpen(true)}>
              <Plus className="h-3.5 w-3.5" /> Add agent
            </Button>
          )}
        </div>
      </div>

      {enrollOpen && <EnrollAgentModal onClose={() => setEnrollOpen(false)} />}

      {hosts.length > 0 && (
        <div className="flex flex-col gap-3 sm:flex-row sm:items-end sm:gap-4">
          <div className="flex-1">
            <Field label="Search" hint="Matches hostname, label, or tag (case-insensitive).">
              <TextInput
                type="search"
                placeholder="hostname, label, or #tag…"
                value={search}
                onChange={(e) => setSearch(e.currentTarget.value)}
                aria-label="Search hosts"
              />
            </Field>
          </div>
          <div>
            <Field label="Status">
              <div
                role="group"
                aria-label="Filter by status"
                className="inline-flex rounded-md border border-border bg-panel p-0.5"
              >
                {STATUS_FILTERS.map(({ key, label }) => {
                  const active = key === statusFilter;
                  return (
                    <button
                      key={key}
                      type="button"
                      aria-pressed={active}
                      onClick={() => setStatusFilter(key)}
                      className={`rounded px-2.5 py-1 text-xs font-medium transition-colors duration-150 ${
                        active ? "bg-panel-2 text-fg shadow-panel" : "text-fg-subtle hover:text-fg"
                      }`}
                    >
                      {label}
                    </button>
                  );
                })}
              </div>
            </Field>
          </div>
        </div>
      )}

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
        ) : filtered.length === 0 ? (
          <div className="flex flex-col items-center gap-3 px-6 py-12 text-center">
            <Server className="h-10 w-10 text-fg-subtle" />
            <p className="text-sm text-fg-muted">No hosts match your filter.</p>
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
                <TH>Agent</TH>
                <TH>Last seen</TH>
              </tr>
            </THead>
            <TBody>
              {filtered.map((h) => (
                <tr
                  key={h.id}
                  role="link"
                  tabIndex={0}
                  aria-label={`Open host ${hostDisplay(h)}`}
                  className="cursor-pointer transition-colors duration-100 hover:bg-panel-2 focus:outline-none focus-visible:bg-panel-2 focus-visible:ring-2 focus-visible:ring-inset focus-visible:ring-accent/50"
                  onClick={() => navigate(`/hosts/${h.id}`)}
                  onKeyDown={(e) => onRowKeyDown(e, h.id)}
                >
                  <TD><StatusPill status={h.status} /></TD>
                  <TD>
                    <span className="font-medium text-fg">{hostDisplay(h)}</span>
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
                  <TD className="font-mono text-xs text-fg-muted whitespace-nowrap">
                    {h.agent_version || "—"}
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
