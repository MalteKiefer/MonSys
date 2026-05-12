import { ArrowUpCircle, Container } from "lucide-react";

import { formatBytes } from "../../components/Chart";
import {
  Empty,
  Panel,
  PanelBody,
  PanelHeader,
  StatusPill,
  TBody,
  TD,
  TH,
  THead,
  Table,
} from "../../components/ui";
import { useT } from "../../i18n/useT";
import { WorkloadRow } from "../../lib/types";

import { useHostDetail } from "./HostLayout";

// Workloads tab: container/pod inventory observed on the host.
export function Workloads() {
  const { detail } = useHostDetail();
  const { t } = useT(["hostDetail", "common"]);
  const updateCount = detail.workloads.filter((w) => w.update_available).length;
  return (
    <Panel>
      <PanelHeader>
        <div className="flex items-center gap-2">
          <Container className="h-4 w-4 text-fg-muted" />
          <h3 className="text-sm font-semibold">{t("hostDetail:workloads.title", { count: detail.workloads.length })}</h3>
          {updateCount > 0 && (
            <span
              className="inline-flex items-center gap-1 rounded-full bg-amber-500/15 px-2 py-0.5 text-xs font-medium text-amber-300"
              title={t("hostDetail:workloads.updatesBadgeTooltip", { count: updateCount })}
            >
              <ArrowUpCircle className="h-3 w-3" />
              {t("hostDetail:workloads.updatesBadge", { count: updateCount })}
            </span>
          )}
        </div>
      </PanelHeader>
      <PanelBody className="p-0 overflow-x-auto"><WorkloadsTable rows={detail.workloads} /></PanelBody>
    </Panel>
  );
}

function WorkloadsTable({ rows }: { rows: WorkloadRow[] }) {
  const { t } = useT(["hostDetail", "common"]);
  if (rows.length === 0) return <Empty>{t("hostDetail:workloads.noWorkloads")}</Empty>;
  return (
    <Table>
      <THead>
        <tr><TH>{t("hostDetail:workloads.colKind")}</TH><TH>{t("hostDetail:workloads.colName")}</TH><TH>{t("hostDetail:workloads.colImage")}</TH><TH>{t("hostDetail:workloads.colState")}</TH><TH>{t("hostDetail:workloads.colUpdate")}</TH><TH>{t("hostDetail:workloads.colCpu")}</TH><TH>{t("hostDetail:workloads.colMem")}</TH></tr>
      </THead>
      <TBody>
        {rows.map((w) => (
          <tr key={w.id} className="hover:bg-panel-2">
            <TD className="text-fg-muted">{w.kind}</TD>
            <TD className="font-medium">{w.name || w.external_id.substring(0, 12)}</TD>
            <TD className="max-w-xs truncate font-mono text-xs text-fg-muted">{w.image || "—"}</TD>
            <TD><StatusPill status={w.state === "running" ? "ok" : "unknown"}>{w.state}</StatusPill></TD>
            <TD><UpdateBadge row={w} /></TD>
            <TD className="tabular-nums text-fg-muted">{w.cpu_usage_pct.toFixed(1)}%</TD>
            <TD className="tabular-nums text-fg-muted">{formatBytes(w.mem_used_bytes)}</TD>
          </tr>
        ))}
      </TBody>
    </Table>
  );
}

// UpdateBadge renders the "↑" indicator when the agent has observed a newer
// upstream image manifest for the same tag the container was started from.
// Hovering surfaces the current vs latest digest so operators can verify
// what they'd be pulling. Containers we haven't probed yet (e.g. ingest
// happened before the first 6h registry-probe tick) render an em-dash.
function UpdateBadge({ row }: { row: WorkloadRow }) {
  const { t } = useT(["hostDetail", "common"]);
  if (row.update_available) {
    const tooltip = [
      t("hostDetail:workloads.updateAvailable"),
      row.current_digest ? t("hostDetail:workloads.currentDigest", { digest: shortDigest(row.current_digest) }) : "",
      row.latest_digest ? t("hostDetail:workloads.latestDigest", { digest: shortDigest(row.latest_digest) }) : "",
      row.update_checked_at ? t("hostDetail:workloads.checkedAt", { time: row.update_checked_at }) : "",
    ]
      .filter(Boolean)
      .join("\n");
    return (
      <span
        className="inline-flex items-center gap-1 text-amber-400"
        title={tooltip}
      >
        <ArrowUpCircle className="h-4 w-4" />
        <span className="text-xs">{t("hostDetail:workloads.update")}</span>
      </span>
    );
  }
  if (row.current_digest && row.latest_digest) {
    // Probe ran and confirmed up-to-date.
    return <span className="text-xs text-fg-muted" title={t("hostDetail:workloads.upToDateTooltip")}>{t("hostDetail:workloads.upToDate")}</span>;
  }
  return <span className="text-xs text-fg-muted">—</span>;
}

// shortDigest trims a "sha256:abcdef…" digest to a friendly 12-char form for
// tooltips without losing the algorithm prefix.
function shortDigest(d: string): string {
  const i = d.indexOf(":");
  if (i < 0) return d.slice(0, 12);
  const algo = d.slice(0, i + 1);
  const hex = d.slice(i + 1);
  return algo + (hex.length > 12 ? hex.slice(0, 12) + "…" : hex);
}

// TODO(parallel-agent-owns-Hosts.tsx): the per-host count of containers with
// updates should also surface as a small docker-shaped badge in the Hosts
// list "Updates" cell (which a parallel agent owns). Skipped here on purpose
// to avoid stepping on that work.
