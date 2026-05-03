import { useQuery } from "@tanstack/react-query";
import { useNavigate } from "react-router-dom";

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
    return <p className="p-6 text-sm text-zinc-400">Loading hosts…</p>;
  }
  if (error) {
    return <p className="p-6 text-sm text-fail">Failed: {(error as Error).message}</p>;
  }
  const hosts = data?.hosts ?? [];

  return (
    <div className="space-y-4 p-6">
      <h2 className="text-lg font-semibold">Hosts</h2>
      <p className="text-sm text-zinc-400">{hosts.length} known</p>

      <div className="overflow-hidden rounded border border-zinc-800">
        <table className="w-full text-sm">
          <thead className="bg-zinc-900 text-left text-xs uppercase tracking-wider text-zinc-400">
            <tr>
              <th className="px-3 py-2">Status</th>
              <th className="px-3 py-2">Hostname</th>
              <th className="px-3 py-2">Distro</th>
              <th className="px-3 py-2">CPU</th>
              <th className="px-3 py-2">RAM</th>
              <th className="px-3 py-2">Last seen</th>
              <th className="px-3 py-2">Agent</th>
            </tr>
          </thead>
          <tbody className="divide-y divide-zinc-800">
            {hosts.map((h) => (
              <tr
                key={h.id}
                className="cursor-pointer hover:bg-zinc-900/60"
                onClick={() => navigate(`/hosts/${h.id}`)}
              >
                <td className="px-3 py-2">
                  <StatusPill status={h.status} />
                </td>
                <td className="px-3 py-2 font-medium">{h.hostname}</td>
                <td className="px-3 py-2 text-zinc-400">{h.distro || "—"}</td>
                <td className="px-3 py-2 text-zinc-400">{h.cpu_cores} cores</td>
                <td className="px-3 py-2 text-zinc-400">{formatBytes(h.ram_total_bytes)}</td>
                <td className="px-3 py-2 text-zinc-400">{relativeTime(h.last_seen_at)}</td>
                <td className="px-3 py-2 text-zinc-400">{h.agent_version}</td>
              </tr>
            ))}
            {hosts.length === 0 && (
              <tr>
                <td className="px-3 py-6 text-center text-sm text-zinc-500" colSpan={7}>
                  No hosts yet. Issue a bootstrap token via{" "}
                  <code className="font-mono">mon-server --new-token</code>.
                </td>
              </tr>
            )}
          </tbody>
        </table>
      </div>
    </div>
  );
}

function StatusPill({ status }: { status: Host["status"] }) {
  const palette: Record<Host["status"], string> = {
    online: "bg-ok/15 text-ok",
    stale: "bg-stale/15 text-stale",
    offline: "bg-fail/15 text-fail",
    unknown: "bg-zinc-700/40 text-zinc-300",
  };
  return (
    <span className={`inline-flex rounded px-2 py-0.5 text-xs font-medium ${palette[status]}`}>
      {status}
    </span>
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
