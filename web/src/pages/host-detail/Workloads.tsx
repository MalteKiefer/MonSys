import { Container } from "lucide-react";

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
import { WorkloadRow } from "../../lib/types";

import { useHostDetail } from "./HostLayout";

// Workloads tab: container/pod inventory observed on the host.
export function Workloads() {
  const { detail } = useHostDetail();
  return (
    <Panel>
      <PanelHeader>
        <div className="flex items-center gap-2">
          <Container className="h-4 w-4 text-fg-muted" />
          <h3 className="text-sm font-semibold">Containers ({detail.workloads.length})</h3>
        </div>
      </PanelHeader>
      <PanelBody className="p-0 overflow-x-auto"><WorkloadsTable rows={detail.workloads} /></PanelBody>
    </Panel>
  );
}

function WorkloadsTable({ rows }: { rows: WorkloadRow[] }) {
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
