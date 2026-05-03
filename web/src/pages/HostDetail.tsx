import { useQuery } from "@tanstack/react-query";
import { Link, useParams } from "react-router-dom";

import { api } from "../lib/api";
import {
  CrowdsecDecision,
  Fail2banJailInfo,
  FirewallStatus,
  HostDetail as HostDetailT,
  HostSecurity,
  LoginEvent,
  PendingUpdate,
  SystemSample,
} from "../lib/types";

type SystemMetricsResp = { host_id: string; from: string; to: string; samples: SystemSample[] };
type LoginsResp = { host_id: string; since: string; events: LoginEvent[] };
type UpdatesResp = { updates: PendingUpdate[] };

export function HostDetail() {
  const { id = "" } = useParams<{ id: string }>();

  const detail = useQuery({
    queryKey: ["host", id],
    queryFn: () => api<HostDetailT>(`/v1/hosts/${id}`),
    refetchInterval: 30_000,
    enabled: !!id,
  });
  const sys = useQuery({
    queryKey: ["host-system", id],
    queryFn: () => api<SystemMetricsResp>(`/v1/hosts/${id}/metrics/system`),
    refetchInterval: 30_000,
    enabled: !!id,
  });
  const security = useQuery({
    queryKey: ["host-security", id],
    queryFn: () => api<HostSecurity>(`/v1/hosts/${id}/security`),
    enabled: !!id,
  });
  const logins = useQuery({
    queryKey: ["host-logins", id],
    queryFn: () => api<LoginsResp>(`/v1/hosts/${id}/logins?limit=50`),
    enabled: !!id,
  });
  const updates = useQuery({
    queryKey: ["host-updates", id],
    queryFn: () => api<UpdatesResp>(`/v1/hosts/${id}/packages/updates`),
    enabled: !!id,
  });

  if (detail.isLoading) {
    return <p className="p-6 text-sm text-zinc-400">Loading host…</p>;
  }
  if (detail.error || !detail.data) {
    return (
      <p className="p-6 text-sm text-fail">
        {(detail.error as Error)?.message ?? "host not found"}
      </p>
    );
  }
  const d = detail.data;

  return (
    <div className="mx-auto max-w-6xl space-y-6 p-6">
      <Header detail={d} />

      <Section title="System">
        <SystemPanel latest={sys.data?.samples?.at(-1)} ramTotal={d.host.ram_total_bytes} />
      </Section>

      <Section title={`Disks (${d.disks.length})`}>
        <DisksTable rows={d.disks} />
      </Section>

      <Section title={`Network (${d.nics.length})`}>
        <NicsTable rows={d.nics} />
      </Section>

      <Section title={`Workloads (${d.workloads.length})`}>
        <WorkloadsTable rows={d.workloads} />
      </Section>

      {d.vms.length > 0 && (
        <Section title={`VMs / containers (libvirt + lxc) — ${d.vms.length}`}>
          <VMsTable rows={d.vms} />
        </Section>
      )}

      <Section title={`Users (${d.users.length})`}>
        <UsersTable rows={d.users} />
      </Section>

      <Section title="Security">
        <SecurityPanel data={security.data} />
      </Section>

      <Section title="Recent logins">
        <LoginsTable rows={logins.data?.events ?? []} />
      </Section>

      <Section title="Packages">
        <PackagesPanel summary={d.packages_summary} updates={updates.data?.updates ?? []} repoStates={d.repo_states} />
      </Section>
    </div>
  );
}

function Header({ detail }: { detail: HostDetailT }) {
  const h = detail.host;
  return (
    <header className="rounded-lg border border-zinc-800 bg-zinc-900 p-5">
      <div className="flex items-start justify-between gap-4">
        <div>
          <Link to="/" className="text-xs text-zinc-500 hover:text-zinc-300">
            ← Hosts
          </Link>
          <h1 className="mt-1 flex items-center gap-3 text-xl font-semibold">
            {h.hostname}
            <StatusPill status={h.status} />
          </h1>
          <p className="mt-1 text-sm text-zinc-400">
            {h.distro} · {h.arch} · agent {h.agent_version}
          </p>
        </div>
        <div className="text-right text-xs text-zinc-400">
          <p>last_seen: {relativeTime(h.last_seen_at)}</p>
          {h.status_since && <p>since: {relativeTime(h.status_since)}</p>}
          <p className="font-mono">{h.id}</p>
        </div>
      </div>

      <dl className="mt-4 grid grid-cols-2 gap-x-6 gap-y-2 text-sm md:grid-cols-4">
        <Stat label="CPU cores" value={h.cpu_cores} />
        <Stat label="RAM" value={formatBytes(h.ram_total_bytes)} />
        <Stat label="First seen" value={new Date(h.first_seen_at).toLocaleDateString()} />
        <Stat label="Status since" value={h.status_since ? relativeTime(h.status_since) : "—"} />
      </dl>

      {Object.keys(h.labels).length > 0 && (
        <div className="mt-3 flex flex-wrap gap-1">
          {Object.entries(h.labels).map(([k, v]) => (
            <span key={k} className="rounded bg-zinc-800 px-2 py-0.5 text-xs text-zinc-300">
              {k}={v}
            </span>
          ))}
        </div>
      )}
    </header>
  );
}

function Section({ title, children }: { title: string; children: React.ReactNode }) {
  return (
    <section className="space-y-2">
      <h2 className="text-sm font-semibold uppercase tracking-wider text-zinc-400">{title}</h2>
      <div className="rounded-lg border border-zinc-800 bg-zinc-900 p-4 overflow-x-auto">
        {children}
      </div>
    </section>
  );
}

function Stat({ label, value }: { label: string; value: string | number }) {
  return (
    <div>
      <dt className="text-xs uppercase tracking-wider text-zinc-500">{label}</dt>
      <dd className="text-zinc-200">{value}</dd>
    </div>
  );
}

function SystemPanel({ latest, ramTotal }: { latest?: SystemSample; ramTotal: number }) {
  if (!latest) return <p className="text-sm text-zinc-500">No system samples yet.</p>;
  const ramPct = ramTotal > 0 ? (latest.ram_used_bytes / ramTotal) * 100 : 0;
  return (
    <dl className="grid grid-cols-2 gap-x-6 gap-y-3 text-sm md:grid-cols-4">
      <Stat label="CPU" value={`${latest.cpu_usage_pct.toFixed(1)} %`} />
      <Stat label="Load 1/5/15" value={`${latest.load_1.toFixed(2)} / ${latest.load_5.toFixed(2)} / ${latest.load_15.toFixed(2)}`} />
      <Stat label="RAM used" value={`${formatBytes(latest.ram_used_bytes)} (${ramPct.toFixed(0)} %)`} />
      <Stat label="Swap used" value={formatBytes(latest.swap_used_bytes)} />
      <Stat label="Uptime" value={formatUptime(latest.uptime_sec)} />
      <Stat label="Sample" value={relativeTime(latest.time)} />
    </dl>
  );
}

function DisksTable({ rows }: { rows: HostDetailT["disks"] }) {
  if (rows.length === 0) return <Empty />;
  return (
    <table className="w-full text-sm">
      <thead className="text-left text-xs uppercase tracking-wider text-zinc-400">
        <tr>
          <th className="px-2 py-1">Mount</th>
          <th className="px-2 py-1">Device</th>
          <th className="px-2 py-1">FS</th>
          <th className="px-2 py-1">Size</th>
          <th className="px-2 py-1">Used</th>
          <th className="px-2 py-1">Free</th>
          <th className="px-2 py-1">Use %</th>
        </tr>
      </thead>
      <tbody className="divide-y divide-zinc-800">
        {rows.map((d) => {
          const usedPct = d.size_bytes > 0 ? (d.used_bytes / d.size_bytes) * 100 : 0;
          return (
            <tr key={d.id}>
              <td className="px-2 py-1 font-mono text-xs">{d.mountpoint}</td>
              <td className="px-2 py-1 font-mono text-xs text-zinc-400">{d.device}</td>
              <td className="px-2 py-1 text-zinc-400">{d.fstype || "—"}</td>
              <td className="px-2 py-1 text-zinc-400">{formatBytes(d.size_bytes)}</td>
              <td className="px-2 py-1 text-zinc-200">{formatBytes(d.used_bytes)}</td>
              <td className="px-2 py-1 text-zinc-400">{formatBytes(d.free_bytes)}</td>
              <td className="px-2 py-1">
                <PercentBar pct={usedPct} />
              </td>
            </tr>
          );
        })}
      </tbody>
    </table>
  );
}

function NicsTable({ rows }: { rows: HostDetailT["nics"] }) {
  if (rows.length === 0) return <Empty />;
  return (
    <table className="w-full text-sm">
      <thead className="text-left text-xs uppercase tracking-wider text-zinc-400">
        <tr>
          <th className="px-2 py-1">Name</th>
          <th className="px-2 py-1">MAC</th>
          <th className="px-2 py-1">Speed</th>
          <th className="px-2 py-1">RX total</th>
          <th className="px-2 py-1">TX total</th>
        </tr>
      </thead>
      <tbody className="divide-y divide-zinc-800">
        {rows.map((n) => (
          <tr key={n.id}>
            <td className="px-2 py-1 font-mono text-xs">{n.name}</td>
            <td className="px-2 py-1 font-mono text-xs text-zinc-400">{n.mac || "—"}</td>
            <td className="px-2 py-1 text-zinc-400">{n.speed_mbps ? `${n.speed_mbps} Mb/s` : "—"}</td>
            <td className="px-2 py-1">{formatBytes(n.rx_bytes)}</td>
            <td className="px-2 py-1">{formatBytes(n.tx_bytes)}</td>
          </tr>
        ))}
      </tbody>
    </table>
  );
}

function WorkloadsTable({ rows }: { rows: HostDetailT["workloads"] }) {
  if (rows.length === 0) return <Empty />;
  return (
    <table className="w-full text-sm">
      <thead className="text-left text-xs uppercase tracking-wider text-zinc-400">
        <tr>
          <th className="px-2 py-1">Kind</th>
          <th className="px-2 py-1">Name</th>
          <th className="px-2 py-1">Image</th>
          <th className="px-2 py-1">State</th>
          <th className="px-2 py-1">CPU</th>
          <th className="px-2 py-1">Mem</th>
        </tr>
      </thead>
      <tbody className="divide-y divide-zinc-800">
        {rows.map((w) => (
          <tr key={w.id}>
            <td className="px-2 py-1">{w.kind}</td>
            <td className="px-2 py-1 font-medium">{w.name || w.external_id.substring(0, 12)}</td>
            <td className="px-2 py-1 text-zinc-400 truncate max-w-xs">{w.image || "—"}</td>
            <td className="px-2 py-1">
              <span className={`rounded px-2 py-0.5 text-xs ${w.state === "running" ? "bg-ok/15 text-ok" : "bg-zinc-700/40 text-zinc-300"}`}>
                {w.state}
              </span>
            </td>
            <td className="px-2 py-1 text-zinc-400">{w.cpu_usage_pct.toFixed(1)} %</td>
            <td className="px-2 py-1 text-zinc-400">{formatBytes(w.mem_used_bytes)}</td>
          </tr>
        ))}
      </tbody>
    </table>
  );
}

function VMsTable({ rows }: { rows: HostDetailT["vms"] }) {
  return (
    <table className="w-full text-sm">
      <thead className="text-left text-xs uppercase tracking-wider text-zinc-400">
        <tr>
          <th className="px-2 py-1">Kind</th>
          <th className="px-2 py-1">Name</th>
          <th className="px-2 py-1">State</th>
          <th className="px-2 py-1">vCPU</th>
          <th className="px-2 py-1">Memory</th>
          <th className="px-2 py-1">Autostart</th>
        </tr>
      </thead>
      <tbody className="divide-y divide-zinc-800">
        {rows.map((v) => (
          <tr key={`${v.kind}-${v.external_id}`}>
            <td className="px-2 py-1">{v.kind}</td>
            <td className="px-2 py-1 font-medium">{v.name}</td>
            <td className="px-2 py-1 text-zinc-400">{v.state}</td>
            <td className="px-2 py-1 text-zinc-400">{v.vcpu}</td>
            <td className="px-2 py-1 text-zinc-400">{formatBytes(v.mem_bytes)}</td>
            <td className="px-2 py-1 text-zinc-400">{v.autostart ? "yes" : "no"}</td>
          </tr>
        ))}
      </tbody>
    </table>
  );
}

function UsersTable({ rows }: { rows: HostDetailT["users"] }) {
  if (rows.length === 0) return <Empty />;
  return (
    <table className="w-full text-sm">
      <thead className="text-left text-xs uppercase tracking-wider text-zinc-400">
        <tr>
          <th className="px-2 py-1">User</th>
          <th className="px-2 py-1">UID</th>
          <th className="px-2 py-1">Shell</th>
          <th className="px-2 py-1">Sudoer</th>
          <th className="px-2 py-1">System</th>
        </tr>
      </thead>
      <tbody className="divide-y divide-zinc-800">
        {rows.map((u) => (
          <tr key={u.username}>
            <td className="px-2 py-1 font-mono">{u.username}</td>
            <td className="px-2 py-1 text-zinc-400">{u.uid}</td>
            <td className="px-2 py-1 text-zinc-500 font-mono text-xs">{u.shell || "—"}</td>
            <td className="px-2 py-1">{u.is_sudoer ? <span className="rounded bg-warn/20 px-2 py-0.5 text-xs text-warn">sudo</span> : "—"}</td>
            <td className="px-2 py-1 text-zinc-500">{u.is_system ? "yes" : "—"}</td>
          </tr>
        ))}
      </tbody>
    </table>
  );
}

function SecurityPanel({ data }: { data?: HostSecurity }) {
  if (!data) return <p className="text-sm text-zinc-500">Loading…</p>;
  return (
    <div className="space-y-4">
      <div>
        <h4 className="mb-2 text-xs uppercase tracking-wider text-zinc-400">Firewalls</h4>
        {data.firewalls.length === 0 ? (
          <Empty />
        ) : (
          <ul className="space-y-1 text-sm">
            {data.firewalls.map((f: FirewallStatus) => (
              <li key={f.engine} className="flex items-center gap-3">
                <span className={`rounded px-2 py-0.5 text-xs ${f.active ? "bg-ok/15 text-ok" : "bg-zinc-700/40 text-zinc-300"}`}>
                  {f.engine}
                </span>
                <span className="text-zinc-400">{f.rule_count} rules</span>
                {f.default_input && <span className="text-zinc-500 font-mono text-xs">in:{f.default_input}</span>}
                {f.default_forward && <span className="text-zinc-500 font-mono text-xs">fwd:{f.default_forward}</span>}
              </li>
            ))}
          </ul>
        )}
      </div>
      <div>
        <h4 className="mb-2 text-xs uppercase tracking-wider text-zinc-400">fail2ban</h4>
        {!data.fail2ban || data.fail2ban.length === 0 ? (
          <p className="text-sm text-zinc-500">No fail2ban data.</p>
        ) : (
          <ul className="text-sm">
            {data.fail2ban.map((j: Fail2banJailInfo) => (
              <li key={j.jail}>
                {j.jail}: {j.currently_banned} banned ({j.total_banned} total), {j.currently_failed}/{j.total_failed} failed
              </li>
            ))}
          </ul>
        )}
      </div>
      <div>
        <h4 className="mb-2 text-xs uppercase tracking-wider text-zinc-400">CrowdSec</h4>
        {!data.crowdsec || data.crowdsec.length === 0 ? (
          <p className="text-sm text-zinc-500">No CrowdSec decisions.</p>
        ) : (
          <ul className="text-sm">
            {data.crowdsec.map((d: CrowdsecDecision) => (
              <li key={d.decision_id}>
                {d.scope} {d.target} ({d.type}) — {d.reason}
              </li>
            ))}
          </ul>
        )}
      </div>
    </div>
  );
}

function LoginsTable({ rows }: { rows: LoginEvent[] }) {
  if (rows.length === 0) return <p className="text-sm text-zinc-500">No login events recorded.</p>;
  return (
    <table className="w-full text-sm">
      <thead className="text-left text-xs uppercase tracking-wider text-zinc-400">
        <tr>
          <th className="px-2 py-1">Time</th>
          <th className="px-2 py-1">User</th>
          <th className="px-2 py-1">From</th>
          <th className="px-2 py-1">Method</th>
          <th className="px-2 py-1">Result</th>
        </tr>
      </thead>
      <tbody className="divide-y divide-zinc-800">
        {rows.map((e, i) => (
          <tr key={i}>
            <td className="px-2 py-1 font-mono text-xs text-zinc-400">{relativeTime(e.time)}</td>
            <td className="px-2 py-1 font-mono">{e.username || "—"}</td>
            <td className="px-2 py-1 font-mono text-xs text-zinc-400">{e.source_ip || "—"}</td>
            <td className="px-2 py-1 text-zinc-400">{e.method}</td>
            <td className="px-2 py-1">
              <span className={`rounded px-2 py-0.5 text-xs ${e.success ? "bg-ok/15 text-ok" : "bg-fail/15 text-fail"}`}>
                {e.success ? "ok" : "fail"}
              </span>
            </td>
          </tr>
        ))}
      </tbody>
    </table>
  );
}

function PackagesPanel({
  summary,
  updates,
  repoStates,
}: {
  summary?: HostDetailT["packages_summary"];
  updates: PendingUpdate[];
  repoStates: HostDetailT["repo_states"];
}) {
  return (
    <div className="space-y-4">
      <dl className="grid grid-cols-2 gap-x-6 gap-y-2 text-sm md:grid-cols-4">
        <Stat label="Installed" value={summary?.installed_count ?? "—"} />
        <Stat label="Updates" value={summary?.updates_count ?? "—"} />
        <Stat label="Security" value={summary?.security_updates ?? "—"} />
        <Stat label="Repo age" value={summary ? `${Math.round((summary.metadata_age_seconds ?? 0) / 3600)} h` : "—"} />
      </dl>

      {repoStates.length > 0 && (
        <div>
          <h4 className="mb-1 text-xs uppercase tracking-wider text-zinc-400">Repos</h4>
          <ul className="text-sm font-mono">
            {repoStates.map((r) => (
              <li key={r.manager}>
                {r.manager}: mtime {relativeTime(r.metadata_mtime)}
              </li>
            ))}
          </ul>
        </div>
      )}

      {updates.length > 0 && (
        <div>
          <h4 className="mb-1 text-xs uppercase tracking-wider text-zinc-400">Pending updates</h4>
          <table className="w-full text-sm">
            <thead className="text-left text-xs uppercase tracking-wider text-zinc-400">
              <tr>
                <th className="px-2 py-1">Manager</th>
                <th className="px-2 py-1">Name</th>
                <th className="px-2 py-1">Current</th>
                <th className="px-2 py-1">Available</th>
                <th className="px-2 py-1">Security</th>
              </tr>
            </thead>
            <tbody className="divide-y divide-zinc-800">
              {updates.map((u, i) => (
                <tr key={i}>
                  <td className="px-2 py-1">{u.manager}</td>
                  <td className="px-2 py-1 font-mono text-xs">{u.name}</td>
                  <td className="px-2 py-1 font-mono text-xs text-zinc-400">{u.current_version}</td>
                  <td className="px-2 py-1 font-mono text-xs">{u.available_version}</td>
                  <td className="px-2 py-1">{u.is_security ? <span className="rounded bg-fail/15 px-2 py-0.5 text-xs text-fail">security</span> : "—"}</td>
                </tr>
              ))}
            </tbody>
          </table>
        </div>
      )}
    </div>
  );
}

// ---- helpers ----

function StatusPill({ status }: { status: string }) {
  const palette: Record<string, string> = {
    online: "bg-ok/15 text-ok",
    stale: "bg-stale/15 text-stale",
    offline: "bg-fail/15 text-fail",
    unknown: "bg-zinc-700/40 text-zinc-300",
  };
  return (
    <span className={`inline-flex rounded px-2 py-0.5 text-xs font-medium ${palette[status] ?? palette.unknown}`}>
      {status}
    </span>
  );
}

function PercentBar({ pct }: { pct: number }) {
  const clipped = Math.max(0, Math.min(100, pct));
  const color = clipped > 90 ? "bg-fail" : clipped > 75 ? "bg-warn" : "bg-ok";
  return (
    <div className="flex items-center gap-2">
      <div className="h-1.5 flex-1 rounded bg-zinc-800">
        <div className={`h-full rounded ${color}`} style={{ width: `${clipped}%` }} />
      </div>
      <span className="w-10 text-right text-xs text-zinc-400">{clipped.toFixed(0)} %</span>
    </div>
  );
}

function Empty() {
  return <p className="text-sm text-zinc-500">No data.</p>;
}

function formatBytes(n: number): string {
  if (!n) return "0";
  const units = ["B", "KiB", "MiB", "GiB", "TiB", "PiB"];
  let v = n;
  let i = 0;
  while (v >= 1024 && i < units.length - 1) {
    v /= 1024;
    i++;
  }
  return `${v.toFixed(v < 10 && i > 0 ? 1 : 0)} ${units[i]}`;
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
