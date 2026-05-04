import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import {
  ArrowLeft,
  Boxes,
  Container,
  Cpu,
  HardDrive,
  LayoutGrid,
  LogIn,
  Network,
  Package,
  ShieldCheck,
  Tag,
  Users as UsersIcon,
  X,
} from "lucide-react";
import { KeyboardEvent, useMemo, useState } from "react";
import { Link, useParams } from "react-router-dom";

import {
  ChartLine,
  ChartSeries,
  colorFor,
  formatBytes,
  formatBytesPerSec,
  formatPercent,
  rateOfChange,
} from "../components/Chart";
import { DistroIcon } from "../components/icons/DistroIcon";
import { ServiceBadges } from "../components/icons/ServiceIcon";
import {
  Empty,
  Panel,
  PanelBody,
  PanelHeader,
  PercentBar,
  Skeleton,
  StatCard,
  StatusPill,
  TBody,
  TD,
  TH,
  THead,
  Table,
  TabItem,
  Tabs,
  TimeRangeSelector,
} from "../components/ui";
import { api } from "../lib/api";
import {
  CrowdsecDecision,
  DiskSample,
  Fail2banJailInfo,
  FirewallStatus,
  HostDetail as HostDetailT,
  HostSecurity,
  LoginEvent,
  NetSample,
  PendingUpdate,
  SystemSample,
} from "../lib/types";

type SystemMetricsResp = { host_id: string; from: string; to: string; samples: SystemSample[] };
type DiskMetricsResp = { host_id: string; from: string; to: string; devices: string[]; samples: DiskSample[] };
type NetMetricsResp = { host_id: string; from: string; to: string; nics: string[]; samples: NetSample[] };
type LoginsResp = { host_id: string; since: string; events: LoginEvent[] };
type UpdatesResp = { updates: PendingUpdate[] };

type TabKey =
  | "overview"
  | "storage"
  | "network"
  | "workloads"
  | "vms"
  | "users"
  | "security"
  | "logins"
  | "packages";

export function HostDetail() {
  const { id = "" } = useParams<{ id: string }>();
  const [tab, setTab] = useState<TabKey>("overview");
  const [rangeSec, setRangeSec] = useState(60 * 60); // 1h default

  const fromTo = useMemo(() => {
    const to = new Date();
    const from = new Date(to.getTime() - rangeSec * 1000);
    return { from: from.toISOString(), to: to.toISOString() };
  }, [rangeSec]);
  const queryStr = `from=${encodeURIComponent(fromTo.from)}&to=${encodeURIComponent(fromTo.to)}`;

  const detail = useQuery({
    queryKey: ["host", id],
    queryFn: () => api<HostDetailT>(`/v1/hosts/${id}`),
    refetchInterval: 30_000,
    enabled: !!id,
  });

  const sys = useQuery({
    queryKey: ["host-system", id, rangeSec],
    queryFn: () => api<SystemMetricsResp>(`/v1/hosts/${id}/metrics/system?${queryStr}`),
    refetchInterval: 15_000,
    enabled: !!id && tab === "overview",
  });
  const disk = useQuery({
    queryKey: ["host-disk", id, rangeSec],
    queryFn: () => api<DiskMetricsResp>(`/v1/hosts/${id}/metrics/disk?${queryStr}`),
    refetchInterval: 30_000,
    enabled: !!id && tab === "storage",
  });
  const net = useQuery({
    queryKey: ["host-net", id, rangeSec],
    queryFn: () => api<NetMetricsResp>(`/v1/hosts/${id}/metrics/net?${queryStr}`),
    refetchInterval: 30_000,
    enabled: !!id && tab === "network",
  });
  const security = useQuery({
    queryKey: ["host-security", id],
    queryFn: () => api<HostSecurity>(`/v1/hosts/${id}/security`),
    enabled: !!id && tab === "security",
  });
  const logins = useQuery({
    queryKey: ["host-logins", id],
    queryFn: () => api<LoginsResp>(`/v1/hosts/${id}/logins?limit=100`),
    enabled: !!id && tab === "logins",
  });
  const updates = useQuery({
    queryKey: ["host-updates", id],
    queryFn: () => api<UpdatesResp>(`/v1/hosts/${id}/packages/updates`),
    enabled: !!id && tab === "packages",
  });

  if (detail.isLoading) {
    return (
      <div className="mx-auto max-w-6xl space-y-4 p-6">
        <Skeleton className="h-44" />
        <Skeleton className="h-10" />
        <Skeleton className="h-72" />
      </div>
    );
  }
  if (detail.error || !detail.data) {
    return <p className="p-6 text-sm text-fail">{(detail.error as Error)?.message ?? "host not found"}</p>;
  }
  const d = detail.data;

  const tabItems: ReadonlyArray<TabItem<TabKey>> = [
    { key: "overview", label: "Overview", icon: LayoutGrid },
    { key: "storage", label: "Storage", icon: HardDrive, badge: d.disks.length || undefined },
    { key: "network", label: "Network", icon: Network, badge: d.nics.length || undefined },
    { key: "workloads", label: "Workloads", icon: Container, badge: d.workloads.length || undefined, hidden: d.workloads.length === 0 },
    { key: "vms", label: "VMs", icon: Boxes, badge: d.vms.length || undefined, hidden: d.vms.length === 0 },
    { key: "users", label: "Users", icon: UsersIcon, badge: d.users.length || undefined },
    { key: "security", label: "Security", icon: ShieldCheck },
    { key: "logins", label: "Logins", icon: LogIn },
    { key: "packages", label: "Packages", icon: Package, badge: d.packages_summary?.installed_count || undefined },
  ];

  return (
    <div className="mx-auto max-w-6xl space-y-5 p-6">
      <Header detail={d} />

      <Tabs items={tabItems} value={tab} onChange={setTab} />

      <div>
        {tab === "overview" && (
          <div className="space-y-5">
            <div className="flex items-center justify-between">
              <h2 className="text-[11px] font-semibold uppercase tracking-wider text-fg-subtle">Live system</h2>
              <TimeRangeSelector value={rangeSec} onChange={setRangeSec} />
            </div>
            <SystemPanel samples={sys.data?.samples ?? []} ramTotal={d.host.ram_total_bytes} loading={sys.isLoading} />
          </div>
        )}

        {tab === "storage" && (
          <div className="space-y-5">
            <div className="flex items-center justify-between">
              <h2 className="text-[11px] font-semibold uppercase tracking-wider text-fg-subtle">I/O over time</h2>
              <TimeRangeSelector value={rangeSec} onChange={setRangeSec} />
            </div>
            <DiskIOPanel samples={disk.data?.samples ?? []} disks={d.disks} loading={disk.isLoading} />
            <Panel>
              <PanelHeader><h3 className="text-sm font-semibold">Mounts ({d.disks.length})</h3></PanelHeader>
              <PanelBody className="p-0 overflow-x-auto"><DisksTable rows={d.disks} /></PanelBody>
            </Panel>
          </div>
        )}

        {tab === "network" && (
          <div className="space-y-5">
            <div className="flex items-center justify-between">
              <h2 className="text-[11px] font-semibold uppercase tracking-wider text-fg-subtle">Throughput over time</h2>
              <TimeRangeSelector value={rangeSec} onChange={setRangeSec} />
            </div>
            <NetIOPanel samples={net.data?.samples ?? []} nics={d.nics} loading={net.isLoading} />
            <Panel>
              <PanelHeader><h3 className="text-sm font-semibold">Interfaces ({d.nics.length})</h3></PanelHeader>
              <PanelBody className="p-0 overflow-x-auto"><NicsTable rows={d.nics} /></PanelBody>
            </Panel>
          </div>
        )}

        {tab === "workloads" && (
          <Panel>
            <PanelHeader>
              <div className="flex items-center gap-2">
                <Container className="h-4 w-4 text-fg-muted" />
                <h3 className="text-sm font-semibold">Containers ({d.workloads.length})</h3>
              </div>
            </PanelHeader>
            <PanelBody className="p-0 overflow-x-auto"><WorkloadsTable rows={d.workloads} /></PanelBody>
          </Panel>
        )}

        {tab === "vms" && (
          <Panel>
            <PanelHeader>
              <div className="flex items-center gap-2">
                <Boxes className="h-4 w-4 text-fg-muted" />
                <h3 className="text-sm font-semibold">Virtual machines / system LXC ({d.vms.length})</h3>
              </div>
            </PanelHeader>
            <PanelBody className="p-0 overflow-x-auto"><VMsTable rows={d.vms} /></PanelBody>
          </Panel>
        )}

        {tab === "users" && (
          <Panel>
            <PanelHeader>
              <div className="flex items-center gap-2">
                <UsersIcon className="h-4 w-4 text-fg-muted" />
                <h3 className="text-sm font-semibold">Local accounts ({d.users.length})</h3>
              </div>
            </PanelHeader>
            <PanelBody className="p-0 overflow-x-auto"><UsersTable rows={d.users} /></PanelBody>
          </Panel>
        )}

        {tab === "security" && (
          <Panel>
            <PanelHeader>
              <div className="flex items-center gap-2">
                <ShieldCheck className="h-4 w-4 text-fg-muted" />
                <h3 className="text-sm font-semibold">Posture</h3>
              </div>
            </PanelHeader>
            <PanelBody><SecurityPanel data={security.data} loading={security.isLoading} /></PanelBody>
          </Panel>
        )}

        {tab === "logins" && (
          <Panel>
            <PanelHeader>
              <div className="flex items-center gap-2">
                <LogIn className="h-4 w-4 text-fg-muted" />
                <h3 className="text-sm font-semibold">Recent auth events</h3>
              </div>
            </PanelHeader>
            <PanelBody className="p-0 overflow-x-auto"><LoginsTable rows={logins.data?.events ?? []} loading={logins.isLoading} /></PanelBody>
          </Panel>
        )}

        {tab === "packages" && (
          <Panel>
            <PanelHeader>
              <div className="flex items-center gap-2">
                <Package className="h-4 w-4 text-fg-muted" />
                <h3 className="text-sm font-semibold">Packages</h3>
                <Link to={`/packages?host_id=${d.host.id}`} className="ml-auto text-xs text-accent hover:underline">
                  Search packages →
                </Link>
              </div>
            </PanelHeader>
            <PanelBody>
              <PackagesPanel summary={d.packages_summary} updates={updates.data?.updates ?? []} repoStates={d.repo_states} />
            </PanelBody>
          </Panel>
        )}
      </div>
    </div>
  );
}

// ---- Header ---------------------------------------------------------------

function Header({ detail }: { detail: HostDetailT }) {
  const h = detail.host;
  return (
    <Panel className="overflow-hidden">
      <div className="p-5">
        <div className="flex items-start justify-between gap-4">
          <div className="min-w-0">
            <Link to="/hosts" className="inline-flex items-center gap-1 text-xs text-fg-subtle hover:text-fg">
              <ArrowLeft className="h-3 w-3" /> Hosts
            </Link>
            <h1 className="mt-1.5 flex items-center gap-2.5 text-xl font-semibold tracking-tight">
              <DistroIcon family={h.distro_family} size={22} />
              <span className="truncate">{h.hostname}</span>
              <StatusPill status={h.status} />
            </h1>
            <p className="mt-1 text-sm text-fg-muted">
              {h.distro} · {h.arch} · agent <span className="font-mono text-xs">{h.agent_version}</span>
            </p>
            {h.services && h.services.length > 0 && (
              <div className="mt-2"><ServiceBadges services={h.services} max={12} /></div>
            )}
          </div>
          <div className="text-right text-xs text-fg-subtle">
            <p>last_seen: <span className="text-fg-muted">{relativeTime(h.last_seen_at)}</span></p>
            {h.status_since && <p>since: <span className="text-fg-muted">{relativeTime(h.status_since)}</span></p>}
            <p className="mt-1 font-mono text-[10px] text-fg-subtle">{h.id}</p>
          </div>
        </div>

        <div className="mt-5 grid grid-cols-2 gap-3 md:grid-cols-4">
          <StatCard label="CPU cores" value={h.cpu_cores} />
          <StatCard label="RAM" value={formatBytes(h.ram_total_bytes)} />
          <StatCard label="First seen" value={new Date(h.first_seen_at).toLocaleDateString()} />
          <StatCard label="Status since" value={h.status_since ? relativeTime(h.status_since) : "—"} />
        </div>

        <div className="mt-4 flex flex-wrap items-start gap-3">
          <TagsEditor hostID={h.id} initial={h.tags} />
          {h.groups.length > 0 && (
            <div className="flex flex-wrap items-center gap-1">
              <span className="text-[11px] font-medium uppercase tracking-wider text-fg-subtle">Groups:</span>
              {h.groups.map((g) => (
                <span key={g.id} className="rounded-md bg-info/10 px-2 py-0.5 text-[11px] font-mono text-info ring-1 ring-inset ring-info/30">
                  {g.name}
                </span>
              ))}
            </div>
          )}
        </div>

        {Object.keys(h.labels).length > 0 && (
          <div className="mt-3 flex flex-wrap gap-1.5">
            {Object.entries(h.labels).map(([k, v]) => (
              <span key={k} className="rounded-md bg-panel-2 px-2 py-0.5 text-[11px] font-mono text-fg-muted">
                {k}={v}
              </span>
            ))}
          </div>
        )}
      </div>
    </Panel>
  );
}

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

// ---- Charts (unchanged math, lifted into per-tab body) -------------------

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

function DiskIOPanel({ samples, disks, loading }: { samples: DiskSample[]; disks: HostDetailT["disks"]; loading: boolean }) {
  const { matrix, series } = useMemo(() => {
    if (samples.length === 0) return { matrix: [[]], series: [] as ChartSeries[] };
    const byMount = new Map<string, { times: number[]; read: number[]; write: number[] }>();
    for (const s of samples) {
      const t = Math.floor(new Date(s.time).getTime() / 1000);
      const cur = byMount.get(s.mountpoint) ?? { times: [], read: [], write: [] };
      cur.times.push(t);
      cur.read.push(s.read_bytes);
      cur.write.push(s.write_bytes);
      byMount.set(s.mountpoint, cur);
    }
    const timeSet = new Set<number>();
    byMount.forEach((v) => v.times.forEach((t) => timeSet.add(t)));
    const times = Array.from(timeSet).sort((a, b) => a - b);
    const seriesArr: ChartSeries[] = [];
    const cols: number[][] = [times];
    let i = 0;
    byMount.forEach((v, mount) => {
      cols.push(alignToAxis(times, v.times, rateOfChange(v.times, v.read)));
      cols.push(alignToAxis(times, v.times, rateOfChange(v.times, v.write)));
      seriesArr.push({ label: `${mount} read`, color: colorFor(i * 2) });
      seriesArr.push({ label: `${mount} write`, color: colorFor(i * 2 + 1) });
      i++;
    });
    return { matrix: cols, series: seriesArr };
  }, [samples]);

  return (
    <Panel>
      <PanelHeader>
        <div className="flex items-center gap-2">
          <HardDrive className="h-4 w-4 text-fg-muted" />
          <h3 className="text-sm font-semibold">Disk I/O</h3>
          <span className="ml-2 text-xs text-fg-subtle">{disks.length} disks · per-second rate</span>
        </div>
      </PanelHeader>
      <PanelBody>
        {loading && samples.length === 0 ? (
          <Skeleton className="h-48" />
        ) : samples.length === 0 ? (
          <Empty>No disk samples in this range.</Empty>
        ) : (
          <ChartLine data={{ matrix }} series={series} formatY={formatBytesPerSec} yMin={0} height={220} />
        )}
      </PanelBody>
    </Panel>
  );
}

function NetIOPanel({ samples, nics, loading }: { samples: NetSample[]; nics: HostDetailT["nics"]; loading: boolean }) {
  const { matrix, series } = useMemo(() => {
    if (samples.length === 0) return { matrix: [[]], series: [] as ChartSeries[] };
    const byNic = new Map<string, { times: number[]; rx: number[]; tx: number[] }>();
    for (const s of samples) {
      const t = Math.floor(new Date(s.time).getTime() / 1000);
      const cur = byNic.get(s.nic_name) ?? { times: [], rx: [], tx: [] };
      cur.times.push(t);
      cur.rx.push(s.rx_bytes);
      cur.tx.push(s.tx_bytes);
      byNic.set(s.nic_name, cur);
    }
    const timeSet = new Set<number>();
    byNic.forEach((v) => v.times.forEach((t) => timeSet.add(t)));
    const times = Array.from(timeSet).sort((a, b) => a - b);
    const cols: number[][] = [times];
    const seriesArr: ChartSeries[] = [];
    let i = 0;
    byNic.forEach((v, name) => {
      cols.push(alignToAxis(times, v.times, rateOfChange(v.times, v.rx)));
      cols.push(alignToAxis(times, v.times, rateOfChange(v.times, v.tx)));
      seriesArr.push({ label: `${name} rx`, color: colorFor(i * 2) });
      seriesArr.push({ label: `${name} tx`, color: colorFor(i * 2 + 1) });
      i++;
    });
    return { matrix: cols, series: seriesArr };
  }, [samples]);

  return (
    <Panel>
      <PanelHeader>
        <div className="flex items-center gap-2">
          <Network className="h-4 w-4 text-fg-muted" />
          <h3 className="text-sm font-semibold">Network I/O</h3>
          <span className="ml-2 text-xs text-fg-subtle">{nics.length} nics · per-second rate</span>
        </div>
      </PanelHeader>
      <PanelBody>
        {loading && samples.length === 0 ? (
          <Skeleton className="h-48" />
        ) : samples.length === 0 ? (
          <Empty>No network samples in this range.</Empty>
        ) : (
          <ChartLine data={{ matrix }} series={series} formatY={formatBytesPerSec} yMin={0} height={220} />
        )}
      </PanelBody>
    </Panel>
  );
}

function alignToAxis(target: number[], src: number[], values: number[]): number[] {
  const out = new Array(target.length).fill(0);
  let j = 0;
  for (let i = 0; i < target.length; i++) {
    while (j < src.length && src[j] < target[i]) j++;
    if (j < src.length && src[j] === target[i]) {
      out[i] = values[j];
    }
  }
  return out;
}

// ---- Tables ---------------------------------------------------------------

function DisksTable({ rows }: { rows: HostDetailT["disks"] }) {
  if (rows.length === 0) return <Empty>No disks.</Empty>;
  return (
    <Table>
      <THead>
        <tr><TH>Mount</TH><TH>Device</TH><TH>FS</TH><TH>Size</TH><TH>Used</TH><TH>Free</TH><TH>Use</TH></tr>
      </THead>
      <TBody>
        {rows.map((d) => {
          const usedPct = d.size_bytes > 0 ? (d.used_bytes / d.size_bytes) * 100 : 0;
          return (
            <tr key={d.id} className="hover:bg-panel-2">
              <TD className="font-mono text-xs">{d.mountpoint}</TD>
              <TD className="font-mono text-xs text-fg-muted">{d.device}</TD>
              <TD className="text-fg-muted">{d.fstype || "—"}</TD>
              <TD className="tabular-nums text-fg-muted">{formatBytes(d.size_bytes)}</TD>
              <TD className="tabular-nums">{formatBytes(d.used_bytes)}</TD>
              <TD className="tabular-nums text-fg-muted">{formatBytes(d.free_bytes)}</TD>
              <TD><PercentBar pct={usedPct} /></TD>
            </tr>
          );
        })}
      </TBody>
    </Table>
  );
}

function NicsTable({ rows }: { rows: HostDetailT["nics"] }) {
  if (rows.length === 0) return <Empty>No NICs.</Empty>;
  return (
    <Table>
      <THead>
        <tr><TH>Name</TH><TH>MAC</TH><TH>Addresses</TH><TH>Speed</TH><TH>RX total</TH><TH>TX total</TH></tr>
      </THead>
      <TBody>
        {rows.map((n) => {
          const addrs = (n.addrs ?? []).filter(Boolean);
          const v4 = addrs.filter((a) => !a.includes(":"));
          const v6 = addrs.filter((a) => a.includes(":"));
          return (
            <tr key={n.id} className="hover:bg-panel-2 align-top">
              <TD className="font-mono text-xs">{n.name}</TD>
              <TD className="font-mono text-xs text-fg-muted">{n.mac || "—"}</TD>
              <TD className="font-mono text-xs">
                {addrs.length === 0 ? (
                  <span className="text-fg-subtle">—</span>
                ) : (
                  <div className="space-y-0.5">
                    {v4.map((a) => (
                      <div key={a} title="IPv4">{a}</div>
                    ))}
                    {v6.map((a) => (
                      <div key={a} className="text-fg-muted" title="IPv6">{a}</div>
                    ))}
                  </div>
                )}
              </TD>
              <TD className="text-fg-muted">{n.speed_mbps ? `${n.speed_mbps} Mb/s` : "—"}</TD>
              <TD className="tabular-nums">{formatBytes(n.rx_bytes)}</TD>
              <TD className="tabular-nums">{formatBytes(n.tx_bytes)}</TD>
            </tr>
          );
        })}
      </TBody>
    </Table>
  );
}

function WorkloadsTable({ rows }: { rows: HostDetailT["workloads"] }) {
  if (rows.length === 0) return <Empty>No workloads.</Empty>;
  return (
    <Table>
      <THead>
        <tr><TH>Kind</TH><TH>Name</TH><TH>Image</TH><TH>State</TH><TH>CPU</TH><TH>Mem</TH></tr>
      </THead>
      <TBody>
        {rows.map((w) => (
          <tr key={w.id} className="hover:bg-panel-2">
            <TD className="text-fg-muted">{w.kind}</TD>
            <TD className="font-medium">{w.name || w.external_id.substring(0, 12)}</TD>
            <TD className="max-w-xs truncate font-mono text-xs text-fg-muted">{w.image || "—"}</TD>
            <TD><StatusPill status={w.state === "running" ? "ok" : "unknown"}>{w.state}</StatusPill></TD>
            <TD className="tabular-nums text-fg-muted">{w.cpu_usage_pct.toFixed(1)}%</TD>
            <TD className="tabular-nums text-fg-muted">{formatBytes(w.mem_used_bytes)}</TD>
          </tr>
        ))}
      </TBody>
    </Table>
  );
}

function VMsTable({ rows }: { rows: HostDetailT["vms"] }) {
  if (rows.length === 0) return <Empty>No VMs detected.</Empty>;
  return (
    <Table>
      <THead>
        <tr><TH>Kind</TH><TH>Name</TH><TH>State</TH><TH>vCPU</TH><TH>Memory</TH><TH>Autostart</TH></tr>
      </THead>
      <TBody>
        {rows.map((v) => (
          <tr key={`${v.kind}-${v.external_id}`} className="hover:bg-panel-2">
            <TD className="text-fg-muted">{v.kind}</TD>
            <TD className="font-medium">{v.name}</TD>
            <TD className="text-fg-muted">{v.state}</TD>
            <TD className="tabular-nums text-fg-muted">{v.vcpu}</TD>
            <TD className="tabular-nums text-fg-muted">{formatBytes(v.mem_bytes)}</TD>
            <TD className="text-fg-muted">{v.autostart ? "yes" : "no"}</TD>
          </tr>
        ))}
      </TBody>
    </Table>
  );
}

function UsersTable({ rows }: { rows: HostDetailT["users"] }) {
  if (rows.length === 0) return <Empty>No observed users.</Empty>;
  return (
    <Table>
      <THead>
        <tr><TH>User</TH><TH>UID</TH><TH>Shell</TH><TH>Sudoer</TH><TH>System</TH><TH>Last login</TH></tr>
      </THead>
      <TBody>
        {rows.map((u) => (
          <tr key={u.username} className="hover:bg-panel-2">
            <TD className="font-mono text-fg">{u.username}</TD>
            <TD className="tabular-nums text-fg-muted">{u.uid}</TD>
            <TD className="font-mono text-xs text-fg-muted">{u.shell || "—"}</TD>
            <TD>{u.is_sudoer ? <StatusPill status="warn">sudo</StatusPill> : <span className="text-fg-subtle">—</span>}</TD>
            <TD className="text-fg-subtle">{u.is_system ? "yes" : "—"}</TD>
            <TD className="text-fg-muted text-xs">{u.last_login_at ? relativeTime(u.last_login_at) : "—"}</TD>
          </tr>
        ))}
      </TBody>
    </Table>
  );
}

function SecurityPanel({ data, loading }: { data?: HostSecurity; loading: boolean }) {
  if (loading || !data) return <Skeleton className="h-32" />;
  return (
    <div className="grid gap-5 md:grid-cols-3">
      <div>
        <h4 className="mb-2 text-[11px] font-semibold uppercase tracking-wider text-fg-subtle">Firewalls</h4>
        {data.firewalls.length === 0 ? <p className="text-sm text-fg-subtle">None detected.</p> : (
          <ul className="space-y-1 text-sm">
            {data.firewalls.map((f: FirewallStatus) => (
              <li key={f.engine} className="flex items-center gap-2">
                <StatusPill status={f.active ? "ok" : "unknown"}>{f.engine}</StatusPill>
                <span className="text-fg-muted">{f.rule_count} rules</span>
                {f.default_input && <span className="font-mono text-xs text-fg-subtle">in:{f.default_input}</span>}
                {f.default_forward && <span className="font-mono text-xs text-fg-subtle">fwd:{f.default_forward}</span>}
              </li>
            ))}
          </ul>
        )}
      </div>
      <div>
        <h4 className="mb-2 text-[11px] font-semibold uppercase tracking-wider text-fg-subtle">fail2ban</h4>
        {!data.fail2ban || data.fail2ban.length === 0 ? <p className="text-sm text-fg-subtle">No fail2ban data.</p> : (
          <ul className="space-y-1 text-sm">
            {data.fail2ban.map((j: Fail2banJailInfo) => (
              <li key={j.jail} className="text-fg-muted">
                <span className="font-mono">{j.jail}</span>: {j.currently_banned} banned · {j.currently_failed}/{j.total_failed} failed
              </li>
            ))}
          </ul>
        )}
      </div>
      <div>
        <h4 className="mb-2 text-[11px] font-semibold uppercase tracking-wider text-fg-subtle">CrowdSec</h4>
        {!data.crowdsec || data.crowdsec.length === 0 ? <p className="text-sm text-fg-subtle">No decisions.</p> : (
          <ul className="space-y-1 text-sm">
            {data.crowdsec.map((d: CrowdsecDecision) => (
              <li key={d.decision_id} className="text-fg-muted">
                <span className="font-mono">{d.scope} {d.target}</span> ({d.type})
              </li>
            ))}
          </ul>
        )}
      </div>
    </div>
  );
}

function LoginsTable({ rows, loading }: { rows: LoginEvent[]; loading: boolean }) {
  if (loading) return <Skeleton className="m-5 h-32" />;
  if (rows.length === 0) return <Empty>No login events recorded.</Empty>;
  return (
    <Table>
      <THead>
        <tr><TH>Time</TH><TH>User</TH><TH>From</TH><TH>Method</TH><TH>Result</TH></tr>
      </THead>
      <TBody>
        {rows.map((e, i) => (
          <tr key={i} className="hover:bg-panel-2">
            <TD className="font-mono text-xs text-fg-muted">{relativeTime(e.time)}</TD>
            <TD className="font-mono">{e.username || "—"}</TD>
            <TD className="font-mono text-xs text-fg-muted">{e.source_ip || "—"}</TD>
            <TD className="text-fg-muted">{e.method}</TD>
            <TD><StatusPill status={e.success ? "ok" : "fail"}>{e.success ? "ok" : "fail"}</StatusPill></TD>
          </tr>
        ))}
      </TBody>
    </Table>
  );
}

function PackagesPanel({
  summary, updates, repoStates,
}: {
  summary?: HostDetailT["packages_summary"];
  updates: PendingUpdate[];
  repoStates: HostDetailT["repo_states"];
}) {
  return (
    <div className="space-y-4">
      <div className="grid grid-cols-2 gap-3 md:grid-cols-4">
        <StatCard label="Installed" value={summary?.installed_count ?? "—"} />
        <StatCard label="Updates" value={summary?.updates_count ?? "—"} />
        <StatCard label="Security" value={summary?.security_updates ?? "—"} />
        <StatCard label="Repo age" value={summary ? `${Math.round((summary.metadata_age_seconds ?? 0) / 3600)} h` : "—"} />
      </div>
      {repoStates.length > 0 && (
        <div>
          <h4 className="mb-1 text-[11px] font-semibold uppercase tracking-wider text-fg-subtle">Repos</h4>
          <ul className="space-y-0.5 text-sm font-mono">
            {repoStates.map((r) => (
              <li key={r.manager} className="text-fg-muted">{r.manager}: mtime {relativeTime(r.metadata_mtime)}</li>
            ))}
          </ul>
        </div>
      )}
      {updates.length > 0 && (
        <div>
          <h4 className="mb-1 text-[11px] font-semibold uppercase tracking-wider text-fg-subtle">Pending updates</h4>
          <Table>
            <THead>
              <tr><TH>Manager</TH><TH>Name</TH><TH>Current</TH><TH>Available</TH><TH>Security</TH></tr>
            </THead>
            <TBody>
              {updates.map((u, i) => (
                <tr key={i} className="hover:bg-panel-2">
                  <TD className="text-fg-muted">{u.manager}</TD>
                  <TD className="font-mono text-xs">{u.name}</TD>
                  <TD className="font-mono text-xs text-fg-muted">{u.current_version}</TD>
                  <TD className="font-mono text-xs">{u.available_version}</TD>
                  <TD>{u.is_security ? <StatusPill status="fail">security</StatusPill> : <span className="text-fg-subtle">—</span>}</TD>
                </tr>
              ))}
            </TBody>
          </Table>
        </div>
      )}
    </div>
  );
}

// ---- Helpers --------------------------------------------------------------

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
