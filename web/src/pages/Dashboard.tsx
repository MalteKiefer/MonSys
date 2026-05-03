import { useQuery } from "@tanstack/react-query";
import { Activity, Bell, ServerCrash, ShieldAlert } from "lucide-react";
import { Link } from "react-router-dom";

import {
  Empty,
  Panel,
  PanelBody,
  PanelHeader,
  StatCard,
  StatusPill,
  TBody,
  TD,
  TH,
  THead,
  Table,
} from "../components/ui";
import { api } from "../lib/api";
import { AlertHistoryEntry, Host, Monitor } from "../lib/types";

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
  const since = new Date(Date.now() - 24 * 60 * 60 * 1000).toISOString();
  const alerts = useQuery({
    queryKey: ["alerts-24h"],
    queryFn: () =>
      api<{ alerts: AlertHistoryEntry[] }>(
        `/v1/notifications/alerts?since=${encodeURIComponent(since)}&limit=20`,
      ),
    refetchInterval: 30_000,
  });

  const list = hosts.data?.hosts ?? [];
  const counts = {
    total: list.length,
    online: list.filter((h) => h.status === "online").length,
    stale: list.filter((h) => h.status === "stale").length,
    offline: list.filter((h) => h.status === "offline").length,
  };

  const failingMonitors = (monitors.data?.monitors ?? []).filter(
    (m) => m.last_status === "fail" || m.last_status === "warn",
  );

  return (
    <div className="mx-auto max-w-6xl space-y-6 p-6">
      <header>
        <h1 className="text-lg font-semibold tracking-tight">Overview</h1>
        <p className="text-sm text-fg-muted">Fleet status at a glance.</p>
      </header>

      <div className="grid grid-cols-2 gap-3 md:grid-cols-4">
        <StatCard label="Hosts" value={counts.total} />
        <StatCard label="Online" value={<span className="text-ok">{counts.online}</span>} />
        <StatCard label="Stale" value={<span className="text-warn">{counts.stale}</span>} />
        <StatCard label="Offline" value={<span className="text-fail">{counts.offline}</span>} />
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
                  <tr>
                    <TH>Status</TH>
                    <TH>Host</TH>
                    <TH>Last seen</TH>
                  </tr>
                </THead>
                <TBody>
                  {list
                    .filter((h) => h.status !== "online")
                    .map((h) => (
                      <tr key={h.id} className="hover:bg-panel-2">
                        <TD><StatusPill status={h.status} /></TD>
                        <TD>
                          <Link to={`/hosts/${h.id}`} className="font-medium text-fg hover:underline">
                            {h.hostname}
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
                  <tr>
                    <TH>Status</TH>
                    <TH>Monitor</TH>
                    <TH>Last detail</TH>
                  </tr>
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
                <tr>
                  <TH>When</TH>
                  <TH>Severity</TH>
                  <TH>Rule</TH>
                  <TH>Subject</TH>
                </tr>
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

function relTime(iso: string): string {
  const t = new Date(iso).getTime();
  if (Number.isNaN(t)) return iso;
  const diff = (Date.now() - t) / 1000;
  if (diff < 60) return `${Math.round(diff)}s ago`;
  if (diff < 3600) return `${Math.round(diff / 60)}m ago`;
  if (diff < 86400) return `${Math.round(diff / 3600)}h ago`;
  return `${Math.round(diff / 86400)}d ago`;
}
