import { useQuery } from "@tanstack/react-query";
import { Activity, AlertTriangle, Bell, CheckCircle2, Cpu, HardDrive, MemoryStick, Server, ServerCrash, ShieldAlert, Sparkles } from "lucide-react";
import { useMemo } from "react";
import { Link } from "react-router-dom";

import { DistroIcon } from "../components/icons/DistroIcon";
import { ServiceBadge } from "../components/icons/ServiceIcon";
import {
  Dot,
  Empty,
  Panel,
  PanelBody,
  PanelHeader,
  Skeleton,
  StatusPill,
  TBody,
  TD,
  TH,
  THead,
  Table,
} from "../components/ui";
import { api } from "../lib/api";
import { AlertHistoryEntry, Host, Monitor } from "../lib/types";
import { hostDisplay } from "../lib/utils";

export function Dashboard() {
  const hosts = useQuery({
    queryKey: ["hosts"],
    queryFn: () => api<{ hosts: Host[] }>("/v1/hosts"),
    refetchInterval: 15_000,
  });
  const monitors = useQuery({
    queryKey: ["monitors"],
    queryFn: () => api<{ monitors: Monitor[] }>("/v1/monitors"),
    refetchInterval: 30_000,
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
  const fleet = useMemo(() => {
    let cores = 0;
    let ram = 0;
    for (const h of list) {
      cores += h.cpu_cores;
      ram += h.ram_total_bytes;
    }
    return { cores, ram };
  }, [list]);

  const services = useMemo(() => aggregate(list, (h) => h.services ?? []), [list]);
  const distros = useMemo(() => aggregate(list, (h) => (h.distro_family ? [h.distro_family] : [])), [list]);

  const failingMonitors = (monitors.data?.monitors ?? []).filter(
    (m) => m.last_status === "fail" || m.last_status === "warn",
  );
  const monitorTotals = useMemo(() => {
    const ms = monitors.data?.monitors ?? [];
    return {
      total: ms.length,
      ok: ms.filter((m) => m.last_status === "ok").length,
      warn: ms.filter((m) => m.last_status === "warn").length,
      fail: ms.filter((m) => m.last_status === "fail").length,
    };
  }, [monitors.data]);

  if (hosts.isLoading) return <DashboardSkeleton />;

  return (
    <div className="mx-auto max-w-7xl space-y-6 p-6">
      <header>
        <h1 className="text-xl font-semibold tracking-tight">Overview</h1>
        <p className="text-sm text-fg-muted">Fleet status at a glance.</p>
      </header>

      {/* Hero status row */}
      <div className="grid gap-3 md:grid-cols-4">
        <HeroCard
          icon={Server}
          label="Hosts"
          value={counts.total}
          tone="neutral"
          accent={
            <span className="inline-flex items-center gap-1 text-[11px] font-medium tabular-nums text-fg-muted">
              <Dot status="online" pulse />
              {counts.online} live
            </span>
          }
        />
        <HeroCard icon={CheckCircle2} label="Online" value={counts.online} tone="ok" />
        <HeroCard icon={AlertTriangle} label="Stale / Offline" value={counts.stale + counts.offline} tone={counts.stale + counts.offline > 0 ? "warn" : "neutral"} />
        <HeroCard
          icon={Sparkles}
          label="24h alerts"
          value={alerts.data?.alerts.length ?? 0}
          tone={(alerts.data?.alerts.length ?? 0) > 0 ? "warn" : "neutral"}
        />
      </div>

      {/* Fleet aggregates + monitors strip */}
      <div className="grid gap-3 md:grid-cols-3">
        <SmallCard icon={Cpu} label="Total cores" value={fleet.cores} />
        <SmallCard icon={MemoryStick} label="Total RAM" value={formatBytes(fleet.ram)} />
        <Panel className="px-4 py-3">
          <p className="text-[11px] font-semibold uppercase tracking-wider text-fg-subtle">Monitors</p>
          <div className="mt-1 flex items-center gap-3 text-sm tabular-nums">
            <span className="inline-flex items-center gap-1 text-ok"><Dot status="ok" /> {monitorTotals.ok}</span>
            <span className="inline-flex items-center gap-1 text-warn"><Dot status="warn" /> {monitorTotals.warn}</span>
            <span className="inline-flex items-center gap-1 text-fail"><Dot status="fail" /> {monitorTotals.fail}</span>
            <span className="ml-auto text-fg-subtle">{monitorTotals.total} total</span>
          </div>
        </Panel>
      </div>

      <div className="grid gap-5 lg:grid-cols-2">
        <Panel>
          <PanelHeader>
            <div className="flex items-center gap-2">
              <ServerCrash className="h-4 w-4 text-fg-muted" />
              <h3 className="text-sm font-semibold">Hosts not online</h3>
            </div>
            <Link to="/hosts" className="text-xs text-accent hover:underline">All hosts →</Link>
          </PanelHeader>
          <PanelBody className="p-0 overflow-x-auto">
            {list.filter((h) => h.status !== "online").length === 0 ? (
              <p className="px-5 py-6 text-center text-sm text-fg-subtle">All hosts online.</p>
            ) : (
              <Table>
                <THead>
                  <tr><TH>Status</TH><TH>Host</TH><TH>Last seen</TH></tr>
                </THead>
                <TBody>
                  {list.filter((h) => h.status !== "online").map((h) => (
                    <tr key={h.id} className="hover:bg-panel-2">
                      <TD><StatusPill status={h.status} /></TD>
                      <TD>
                        <Link to={`/hosts/${h.id}`} className="inline-flex items-center gap-2 font-medium text-fg hover:underline">
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
              <ShieldAlert className="h-4 w-4 text-fg-muted" />
              <h3 className="text-sm font-semibold">Failing monitors</h3>
            </div>
            <Link to="/monitors" className="text-xs text-accent hover:underline">All monitors →</Link>
          </PanelHeader>
          <PanelBody className="p-0 overflow-x-auto">
            {failingMonitors.length === 0 ? (
              <p className="px-5 py-6 text-center text-sm text-fg-subtle">All monitors green.</p>
            ) : (
              <Table>
                <THead>
                  <tr><TH>Status</TH><TH>Monitor</TH><TH>Last detail</TH></tr>
                </THead>
                <TBody>
                  {failingMonitors.map((m) => (
                    <tr key={m.id} className="hover:bg-panel-2">
                      <TD><StatusPill status={m.last_status ?? "unknown"}>{m.last_status}</StatusPill></TD>
                      <TD className="font-medium">{m.type} / {m.name}</TD>
                      <TD className="font-mono text-xs text-fg-subtle truncate max-w-xs">{m.last_detail ?? "—"}</TD>
                    </tr>
                  ))}
                </TBody>
              </Table>
            )}
          </PanelBody>
        </Panel>
      </div>

      <div className="grid gap-5 lg:grid-cols-2">
        <DistributionPanel
          title="Services across fleet"
          icon={HardDrive}
          rows={services}
          renderLabel={(name) => <ServiceBadge name={name} />}
          empty="No services detected yet."
        />
        <DistributionPanel
          title="Distros"
          icon={Activity}
          rows={distros}
          renderLabel={(family) => (
            <span className="inline-flex items-center gap-1.5 text-sm text-fg">
              <DistroIcon family={family} size={14} />
              {family}
            </span>
          )}
          empty="No distro info yet."
        />
      </div>

      <Panel>
        <PanelHeader>
          <div className="flex items-center gap-2">
            <Bell className="h-4 w-4 text-fg-muted" />
            <h3 className="text-sm font-semibold">Recent alerts (24h)</h3>
          </div>
          <Link to="/notifications" className="text-xs text-accent hover:underline">All alerts →</Link>
        </PanelHeader>
        <PanelBody className="p-0 overflow-x-auto">
          {(alerts.data?.alerts ?? []).length === 0 ? (
            <Empty>No alerts in the last 24h.</Empty>
          ) : (
            <Table>
              <THead>
                <tr><TH>When</TH><TH>Severity</TH><TH>Rule</TH><TH>Subject</TH></tr>
              </THead>
              <TBody>
                {(alerts.data?.alerts ?? []).map((a) => (
                  <tr key={a.id} className="hover:bg-panel-2">
                    <TD className="font-mono text-xs text-fg-muted">{relTime(a.at)}</TD>
                    <TD>
                      <StatusPill status={a.severity === "info" ? "ok" : a.severity === "warning" ? "warn" : "fail"}>
                        {a.severity}
                      </StatusPill>
                    </TD>
                    <TD className="text-fg-muted">{a.rule_name}</TD>
                    <TD>{a.subject}</TD>
                  </tr>
                ))}
              </TBody>
            </Table>
          )}
        </PanelBody>
      </Panel>

      <p className="flex items-center justify-center gap-1 pt-2 text-xs text-fg-subtle">
        <Activity className="h-3 w-3" />
        Live data refreshes every 15-30 seconds.
      </p>
    </div>
  );
}

// ---- Hero / small / distribution cards -----------------------------------

function HeroCard({
  icon: Icon,
  label,
  value,
  tone,
  accent,
}: {
  icon: typeof Server;
  label: string;
  value: number | string;
  tone: "neutral" | "ok" | "warn" | "fail";
  accent?: React.ReactNode;
}) {
  const ring =
    tone === "ok"
      ? "ring-ok/20 hover:ring-ok/40"
      : tone === "warn"
        ? "ring-warn/20 hover:ring-warn/40"
        : tone === "fail"
          ? "ring-fail/20 hover:ring-fail/40"
          : "ring-border-strong/40 hover:ring-border-strong";
  const valueColor =
    tone === "ok" ? "text-ok" : tone === "warn" ? "text-warn" : tone === "fail" ? "text-fail" : "text-fg";

  return (
    <div className={`group relative overflow-hidden rounded-xl border border-border bg-panel p-4 shadow-panel ring-1 transition-all duration-200 ease-ui ${ring}`}>
      <div className="flex items-start justify-between">
        <div>
          <p className="text-[11px] font-semibold uppercase tracking-wider text-fg-subtle">{label}</p>
          <p className={`mt-1.5 text-3xl font-semibold tabular-nums ${valueColor}`}>{value}</p>
        </div>
        <Icon className="h-4 w-4 text-fg-subtle group-hover:text-fg-muted transition-colors duration-150" />
      </div>
      {accent && <div className="mt-2">{accent}</div>}
      <div
        aria-hidden
        className="pointer-events-none absolute -right-8 -top-8 h-20 w-20 rounded-full bg-gradient-radial opacity-50 blur-2xl"
        style={{
          background:
            tone === "ok"
              ? "radial-gradient(closest-side, rgba(16,185,129,0.18), transparent)"
              : tone === "warn"
                ? "radial-gradient(closest-side, rgba(245,158,11,0.16), transparent)"
                : tone === "fail"
                  ? "radial-gradient(closest-side, rgba(239,68,68,0.18), transparent)"
                  : "radial-gradient(closest-side, rgba(255,255,255,0.06), transparent)",
        }}
      />
    </div>
  );
}

function SmallCard({ icon: Icon, label, value }: { icon: typeof Server; label: string; value: number | string }) {
  return (
    <Panel className="px-4 py-3">
      <div className="flex items-center justify-between">
        <p className="text-[11px] font-semibold uppercase tracking-wider text-fg-subtle">{label}</p>
        <Icon className="h-3.5 w-3.5 text-fg-subtle" />
      </div>
      <p className="mt-1 text-xl font-semibold tabular-nums">{value}</p>
    </Panel>
  );
}

function DistributionPanel({
  title,
  icon: Icon,
  rows,
  renderLabel,
  empty,
}: {
  title: string;
  icon: typeof Server;
  rows: Array<{ key: string; count: number }>;
  renderLabel: (key: string) => React.ReactNode;
  empty: string;
}) {
  const max = rows.reduce((m, r) => Math.max(m, r.count), 0);
  return (
    <Panel>
      <PanelHeader>
        <div className="flex items-center gap-2">
          <Icon className="h-4 w-4 text-fg-muted" />
          <h3 className="text-sm font-semibold">{title}</h3>
        </div>
        <span className="text-xs tabular-nums text-fg-subtle">{rows.length}</span>
      </PanelHeader>
      <PanelBody>
        {rows.length === 0 ? (
          <p className="text-sm text-fg-subtle">{empty}</p>
        ) : (
          <ul className="space-y-2">
            {rows.map((r) => {
              const pct = max > 0 ? (r.count / max) * 100 : 0;
              return (
                <li key={r.key} className="flex items-center gap-3">
                  <div className="w-32 shrink-0">{renderLabel(r.key)}</div>
                  <div className="relative h-2 flex-1 overflow-hidden rounded-full bg-border">
                    <div
                      className="h-full rounded-full bg-accent/70 transition-all duration-200 ease-ui"
                      style={{ width: `${pct}%` }}
                    />
                  </div>
                  <span className="w-10 text-right tabular-nums text-xs text-fg-muted">{r.count}</span>
                </li>
              );
            })}
          </ul>
        )}
      </PanelBody>
    </Panel>
  );
}

function DashboardSkeleton() {
  return (
    <div className="mx-auto max-w-7xl space-y-6 p-6">
      <Skeleton className="h-7 w-40" />
      <div className="grid gap-3 md:grid-cols-4">
        {[0, 1, 2, 3].map((i) => <Skeleton key={i} className="h-24" />)}
      </div>
      <div className="grid gap-3 md:grid-cols-3">
        {[0, 1, 2].map((i) => <Skeleton key={i} className="h-16" />)}
      </div>
      <div className="grid gap-5 lg:grid-cols-2">
        <Skeleton className="h-64" />
        <Skeleton className="h-64" />
      </div>
    </div>
  );
}

// ---- Helpers --------------------------------------------------------------

function aggregate<T>(items: T[], extract: (t: T) => string[]): Array<{ key: string; count: number }> {
  const counts = new Map<string, number>();
  for (const item of items) {
    for (const k of extract(item)) {
      counts.set(k, (counts.get(k) ?? 0) + 1);
    }
  }
  return [...counts.entries()]
    .map(([key, count]) => ({ key, count }))
    .sort((a, b) => b.count - a.count);
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
  if (!n) return "—";
  const u = ["B", "KiB", "MiB", "GiB", "TiB", "PiB"];
  let v = n;
  let i = 0;
  while (v >= 1024 && i < u.length - 1) {
    v /= 1024;
    i++;
  }
  return `${v.toFixed(v < 10 && i > 0 ? 1 : 0)} ${u[i]}`;
}
