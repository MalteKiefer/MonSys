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
import { useT } from "../../i18n/useT";
import { api } from "../../lib/api";
import type { HostDetail as HostDetailT, PendingUpdate } from "../../lib/types";

import { useHostDetail } from "./HostLayout";

type TFn = (key: string, opts?: Record<string, unknown>) => string;

// Packages tab: package summary + repo metadata mtimes + pending updates.
export function Packages() {
  const { detail, hostId } = useHostDetail();
  const { t } = useT(["hostDetail", "common"]);

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
          <h3 className="text-sm font-semibold">{t("hostDetail:packages.title")}</h3>
          <Link to={`/packages?host_id=${detail.host.id}`} className="ml-auto text-xs text-accent hover:underline">
            {t("hostDetail:packages.searchLink")}
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
  const { t } = useT(["hostDetail", "common"]);
  return (
    <div className="space-y-4">
      <div className="grid grid-cols-2 gap-3 md:grid-cols-4">
        <StatCard label={t("hostDetail:packages.statInstalled")} value={summary?.installed_count ?? "—"} />
        <StatCard label={t("hostDetail:packages.statUpdates")} value={summary?.updates_count ?? "—"} />
        <StatCard label={t("hostDetail:packages.statSecurity")} value={summary?.security_updates ?? "—"} />
        <StatCard label={t("hostDetail:packages.statRepoAge")} value={summary ? t("hostDetail:packages.repoAgeValue", { hours: Math.round((summary.metadata_age_seconds ?? 0) / 3600) }) : "—"} />
      </div>
      {repoStates.length > 0 && (
        <div>
          <h4 className="mb-1 text-[11px] font-semibold uppercase tracking-wider text-fg-subtle">{t("hostDetail:packages.reposTitle")}</h4>
          <ul className="space-y-0.5 text-sm font-mono">
            {repoStates.map((r) => (
              <li key={r.manager} className="text-fg-muted">{t("hostDetail:packages.repoLine", { manager: r.manager, age: relativeTime(r.metadata_mtime, t) })}</li>
            ))}
          </ul>
        </div>
      )}
      {updates.length > 0 && (
        <div>
          <h4 className="mb-1 text-[11px] font-semibold uppercase tracking-wider text-fg-subtle">{t("hostDetail:packages.pendingTitle")}</h4>
          <div className="overflow-x-auto">
          <Table>
            <THead>
              <tr><TH>{t("hostDetail:packages.colManager")}</TH><TH>{t("hostDetail:packages.colName")}</TH><TH>{t("hostDetail:packages.colCurrent")}</TH><TH>{t("hostDetail:packages.colAvailable")}</TH><TH>{t("hostDetail:packages.colSecurity")}</TH></tr>
            </THead>
            <TBody>
              {updates.map((u, i) => (
                <tr key={i} className="hover:bg-panel-2">
                  <TD className="text-fg-muted">{u.manager}</TD>
                  <TD className="font-mono text-xs">{u.name}</TD>
                  <TD className="font-mono text-xs text-fg-muted">{u.current_version}</TD>
                  <TD className="font-mono text-xs">{u.available_version}</TD>
                  <TD>{u.is_security ? <StatusPill status="fail">{t("hostDetail:packages.securityBadge")}</StatusPill> : <span className="text-fg-subtle">—</span>}</TD>
                </tr>
              ))}
            </TBody>
          </Table>
          </div>
        </div>
      )}
    </div>
  );
}

function relativeTime(iso: string, t: TFn): string {
  const ts = new Date(iso).getTime();
  if (Number.isNaN(ts)) return iso;
  const diff = (Date.now() - ts) / 1000;
  if (diff < 60) return t("hostDetail:header.secondsAgo", { count: Math.round(diff) });
  if (diff < 3600) return t("hostDetail:header.minutesAgo", { count: Math.round(diff / 60) });
  if (diff < 86400) return t("hostDetail:header.hoursAgo", { count: Math.round(diff / 3600) });
  return t("hostDetail:header.daysAgo", { count: Math.round(diff / 86400) });
}
