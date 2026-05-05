import { useQuery } from "@tanstack/react-query";
import {
  Activity,
  AlertTriangle,
  Bell,
  CheckCircle2,
  ChevronDown,
  Cpu,
  Package as PackageIcon,
  ServerCrash,
} from "lucide-react";
import { useEffect, useMemo, useState } from "react";
import { Link } from "react-router-dom";

import { ChartLine, ChartSeries, colorFor } from "../components/Chart";
import { DistroIcon } from "../components/icons/DistroIcon";
import { Page } from "../components/page";
import {
  Empty,
  Panel,
  PanelBody,
  PanelHeader,
  Skeleton,
  StatCard,
  StatusPill,
  TBody,
  TD,
  TH,
  THead,
  Table,
  TimeRangeSelector,
} from "../components/ui";
import { api } from "../lib/api";
import { AlertHistoryEntry, Host, SystemSample } from "../lib/types";
import { hostDisplay } from "../lib/utils";

type SystemMetricsResp = { host_id: string; from: string; to: string; samples: SystemSample[] };

export function Dashboard() {
  const hosts = useQuery({
    queryKey: ["hosts"],
    queryFn: () => api<{ hosts: Host[] }>("/v1/hosts"),
    refetchInterval: 15_000,
  });
  const since = useMemo(() => new Date(Date.now() - 24 * 60 * 60 * 1000).toISOString(), []);
  const alerts = useQuery({
    queryKey: ["alerts-24h"],
    queryFn: () =>
      api<{ alerts: AlertHistoryEntry[] }>(
        `/v1/notifications/alerts?since=${encodeURIComponent(since)}&limit=20`,
      ),
    refetchInterval: 30_000,
  });

  const list = hosts.data?.hosts ?? [];
  const counts = useMemo(
    () => ({
      total: list.length,
      online: list.filter((h) => h.status === "online").length,
      stale: list.filter((h) => h.status === "stale").length,
      offline: list.filter((h) => h.status === "offline").length,
    }),
    [list],
  );

  // Hosts that need operator attention: offline first, then stale.
  const needAttention = useMemo(() => {
    const off = list.filter((h) => h.status === "offline");
    const stale = list.filter((h) => h.status === "stale");
    return [...off, ...stale];
  }, [list]);

  // Last-10 alert history for the "Recent alerts" panel.
  const recentAlerts = useMemo(
    () => (alerts.data?.alerts ?? []).slice(0, 10),
    [alerts.data?.alerts],
  );

  // Host picker — defaults to first online host once data lands.
  const [hostID, setHostID] = useState<string>("");
  useEffect(() => {
    if (hostID) return;
    const firstOnline = list.find((h) => h.status === "online");
    const fallback = firstOnline ?? list[0];
    if (fallback) setHostID(fallback.id);
  }, [list, hostID]);
  const selectedHost = list.find((h) => h.id === hostID) ?? null;

  const [rangeSec, setRangeSec] = useState(60 * 60);
  const fromTo = useMemo(() => {
    const to = new Date();
    const from = new Date(to.getTime() - rangeSec * 1000);
    return { from: from.toISOString(), to: to.toISOString() };
  }, [rangeSec]);
  const queryStr = `from=${encodeURIComponent(fromTo.from)}&to=${encodeURIComponent(fromTo.to)}`;
  const sys = useQuery({
    queryKey: ["dashboard-system", hostID, rangeSec],
    queryFn: () => api<SystemMetricsResp>(`/v1/hosts/${hostID}/metrics/system?${queryStr}`),
    refetchInterval: 15_000,
    enabled: !!hostID,
  });

  if (hosts.isLoading) return <DashboardSkeleton />;

  return (
    <Page title="Overview" subtitle="Fleet status at a glance.">
      {/* KPIs */}
      <div className="grid gap-3 md:grid-cols-4">
        <StatCard
          label="Online hosts"
          value={
            <span className="inline-flex items-center gap-2">
              <CheckCircle2 className="h-4 w-4 text-ok" />
              {counts.online}
            </span>
          }
          hint={`of ${counts.total} total`}
        />
        <StatCard
          label="Open alerts"
          value={
            <span className="inline-flex items-center gap-2">
              <Bell className={`h-4 w-4 ${(alerts.data?.alerts.length ?? 0) > 0 ? "text-warn" : "text-fg-subtle"}`} />
              {alerts.data?.alerts.length ?? 0}
            </span>
          }
          hint="last 24h"
        />
        <StatCard
          label="Stale hosts"
          value={
            <span className="inline-flex items-center gap-2">
              <AlertTriangle
                className={`h-4 w-4 ${counts.stale + counts.offline > 0 ? "text-warn" : "text-fg-subtle"}`}
              />
              {counts.stale + counts.offline}
            </span>
          }
          hint={`${counts.stale} stale, ${counts.offline} offline`}
        />
        <StatCard
          label="Pending updates"
          value={
            <span className="inline-flex items-center gap-2">
              <PackageIcon className="h-4 w-4 text-fg-subtle" />
              <Link to="/packages" className="text-accent hover:underline">View packages</Link>
            </span>
          }
          hint="per-host detail"
        />
      </div>

      {/* Two-column grid: hosts needing attention + recent alerts. */}
      <div className="grid gap-5 lg:grid-cols-2">
        <Panel>
          <PanelHeader>
            <div className="flex items-center gap-2">
              <ServerCrash className="h-4 w-4 text-fg-muted" />
              <h3 className="text-sm font-semibold">Hosts needing attention</h3>
            </div>
            <Link to="/hosts" className="text-xs text-accent hover:underline">All hosts →</Link>
          </PanelHeader>
          <PanelBody className="p-0 overflow-x-auto">
            {needAttention.length === 0 ? (
              <p className="px-5 py-6 text-center text-sm text-fg-subtle">All hosts online.</p>
            ) : (
              <Table>
                <THead>
                  <tr>
                    <TH>Status</TH>
                    <TH>Host</TH>
                    <TH>Last seen</TH>
                  </tr>
                </THead>
                <TBody>
                  {needAttention.map((h) => (
                    <tr key={h.id} className="hover:bg-panel-2">
                      <TD><StatusPill status={h.status} /></TD>
                      <TD>
                        <Link
                          to={`/hosts/${h.id}`}
                          className="inline-flex items-center gap-2 font-medium text-fg hover:underline"
                        >
                          <DistroIcon family={h.distro_family} size={14} />
                          {hostDisplay(h)}
                        </Link>
                      </TD>
                      <TD className="text-fg-muted">{relTime(h.last_seen_at)}</TD>
                    </tr>
                  ))}
                </TBody>
              </Table>
            )}
          </PanelBody>
        </Panel>

        <Panel>
          <PanelHeader>
            <div className="flex items-center gap-2">
              <Bell className="h-4 w-4 text-fg-muted" />
              <h3 className="text-sm font-semibold">Recent alerts</h3>
            </div>
            <Link to="/notifications" className="text-xs text-accent hover:underline">All alerts →</Link>
          </PanelHeader>
          <PanelBody className="p-0 overflow-x-auto">
            {recentAlerts.length === 0 ? (
              <Empty>No alerts in the last 24h.</Empty>
            ) : (
              <Table>
                <THead>
                  <tr>
                    <TH>When</TH>
                    <TH>Severity</TH>
                    <TH>Subject</TH>
                  </tr>
                </THead>
                <TBody>
                  {recentAlerts.map((a) => (
                    <tr key={a.id} className="hover:bg-panel-2">
                      <TD className="font-mono text-xs text-fg-muted whitespace-nowrap">{relTime(a.at)}</TD>
                      <TD>
                        <StatusPill
                          status={a.severity === "info" ? "ok" : a.severity === "warning" ? "warn" : "fail"}
                        >
                          {a.severity}
                        </StatusPill>
                      </TD>
                      <TD>
                        <Link to="/notifications" className="text-fg hover:text-accent hover:underline">
                          {a.subject}
                        </Link>
                      </TD>
                    </tr>
                  ))}
                </TBody>
              </Table>
            )}
          </PanelBody>
        </Panel>
      </div>

      {/* Charts: only when a host is selected. The picker is here because it
          drives the chart panel below. */}
      <Panel>
        <PanelHeader>
          <div className="flex items-center gap-2">
            <Cpu className="h-4 w-4 text-fg-muted" />
            <h3 className="text-sm font-semibold">Live system</h3>
            {selectedHost && (
              <span className="text-xs text-fg-subtle">· {hostDisplay(selectedHost)}</span>
            )}
          </div>
          <div className="flex items-center gap-2">
            <div className="relative">
              <select
                value={hostID}
                onChange={(e) => setHostID(e.target.value)}
                className="appearance-none rounded-md border border-border bg-panel py-1 pl-2 pr-7 text-xs text-fg focus:border-accent focus:outline-none"
                aria-label="Pick a host for charts"
              >
                <option value="">— pick a host —</option>
                {list.map((h) => (
                  <option key={h.id} value={h.id}>
                    {hostDisplay(h)} {h.status !== "online" ? `(${h.status})` : ""}
                  </option>
                ))}
              </select>
              <ChevronDown className="pointer-events-none absolute right-1.5 top-1/2 h-3.5 w-3.5 -translate-y-1/2 text-fg-subtle" />
            </div>
            {selectedHost && <TimeRangeSelector value={rangeSec} onChange={setRangeSec} />}
          </div>
        </PanelHeader>
        <PanelBody>
          {(() => {
            if (!selectedHost) {
              return (
                <p className="py-6 text-center text-sm text-fg-subtle">
                  Pick a host to display live CPU and RAM charts.
                </p>
              );
            }
            const samples = sys.data?.samples ?? [];
            if (sys.isLoading && samples.length === 0) {
              return <Skeleton className="h-48" />;
            }
            if (samples.length === 0) {
              return <Empty>No system samples in this range.</Empty>;
            }
            return <SystemChart samples={samples} ramTotal={selectedHost.ram_total_bytes} />;
          })()}
        </PanelBody>
      </Panel>

      <p className="flex items-center justify-center gap-1 pt-2 text-xs text-fg-subtle">
        <Activity className="h-3 w-3" />
        Live data refreshes every 15-30 seconds.
      </p>
    </Page>
  );
}

// ---- Chart helper ---------------------------------------------------------

// Renders the same CPU/RAM line chart used on HostDetail's Live System panel.
// The math is a straight port — we deliberately don't share code so this page
// stays editable in isolation.
function SystemChart({ samples, ramTotal }: { samples: SystemSample[]; ramTotal: number }) {
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
    <ChartLine
      data={{ matrix }}
      series={series}
      formatY={(v) => `${v.toFixed(0)}%`}
      yMin={0}
      height={220}
    />
  );
}

function DashboardSkeleton() {
  return (
    <div className="mx-auto max-w-7xl space-y-6 p-6">
      <Skeleton className="h-7 w-40" />
      <div className="grid gap-3 md:grid-cols-4">
        {[0, 1, 2, 3].map((i) => <Skeleton key={i} className="h-24" />)}
      </div>
      <div className="grid gap-5 lg:grid-cols-2">
        <Skeleton className="h-64" />
        <Skeleton className="h-64" />
      </div>
      <Skeleton className="h-72" />
    </div>
  );
}

// ---- Helpers --------------------------------------------------------------

function relTime(iso: string): string {
  const t = new Date(iso).getTime();
  if (Number.isNaN(t)) return iso;
  const diff = (Date.now() - t) / 1000;
  if (diff < 60) return `${Math.round(diff)}s ago`;
  if (diff < 3600) return `${Math.round(diff / 60)}m ago`;
  if (diff < 86400) return `${Math.round(diff / 3600)}h ago`;
  return `${Math.round(diff / 86400)}d ago`;
}
