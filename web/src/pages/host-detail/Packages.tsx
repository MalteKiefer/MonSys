import { useQuery } from "@tanstack/react-query";
import { Package as PackageIcon } from "lucide-react";
import { Link } from "react-router-dom";

import {
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
} from "../../components/ui";
import { api } from "../../lib/api";
import { HostDetail as HostDetailT, PendingUpdate } from "../../lib/types";

import { useHostDetail } from "./HostLayout";

// Packages tab: package summary + repo metadata mtimes + pending updates.
export function Packages() {
  const { detail, hostId } = useHostDetail();

  // Pending updates aren't part of the base host bundle (they can be sizeable
  // and not every page needs them). Fetch only when this tab mounts.
  const updates = useQuery({
    queryKey: ["host-updates", hostId],
    queryFn: () => api<{ updates: PendingUpdate[] }>(`/v1/hosts/${hostId}/packages/updates`),
    enabled: !!hostId,
  });

  return (
    <Panel>
      <PanelHeader>
        <div className="flex items-center gap-2">
          <PackageIcon className="h-4 w-4 text-fg-muted" />
          <h3 className="text-sm font-semibold">Packages</h3>
          <Link to={`/packages?host_id=${detail.host.id}`} className="ml-auto text-xs text-accent hover:underline">
            Search packages →
          </Link>
        </div>
      </PanelHeader>
      <PanelBody>
        <PackagesPanel
          summary={detail.packages_summary}
          updates={updates.data?.updates ?? []}
          repoStates={detail.repo_states}
        />
      </PanelBody>
    </Panel>
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

function relativeTime(iso: string): string {
  const t = new Date(iso).getTime();
  if (Number.isNaN(t)) return iso;
  const diff = (Date.now() - t) / 1000;
  if (diff < 60) return `${Math.round(diff)}s ago`;
  if (diff < 3600) return `${Math.round(diff / 60)}m ago`;
  if (diff < 86400) return `${Math.round(diff / 3600)}h ago`;
  return `${Math.round(diff / 86400)}d ago`;
}
