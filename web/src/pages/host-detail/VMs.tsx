import { Boxes } from "lucide-react";

import { formatBytes } from "../../components/Chart";
import {
  Empty,
  Panel,
  PanelBody,
  PanelHeader,
  TBody,
  TD,
  TH,
  THead,
  Table,
} from "../../components/ui";
import { useT } from "../../i18n/useT";
import type { VMRow } from "../../lib/types";

import { useHostDetail } from "./HostLayout";

// VMs tab: virtual machines and system-level LXC containers (i.e. containers
// the host treats as full VMs rather than application workloads).
export function VMs() {
  const { detail } = useHostDetail();
  const { t } = useT(["hostDetail", "common"]);
  return (
    <Panel>
      <PanelHeader>
        <div className="flex items-center gap-2">
          <Boxes className="h-4 w-4 text-fg-muted" />
          <h3 className="text-sm font-semibold">{t("hostDetail:vms.title", { count: detail.vms.length })}</h3>
        </div>
      </PanelHeader>
      <PanelBody className="p-0 overflow-x-auto"><VMsTable rows={detail.vms} /></PanelBody>
    </Panel>
  );
}

function VMsTable({ rows }: { rows: VMRow[] }) {
  const { t } = useT(["hostDetail", "common"]);
  if (rows.length === 0) return <Empty>{t("hostDetail:vms.noVMs")}</Empty>;
  return (
    <Table>
      <THead>
        <tr><TH>{t("hostDetail:vms.colKind")}</TH><TH>{t("hostDetail:vms.colName")}</TH><TH>{t("hostDetail:vms.colState")}</TH><TH>{t("hostDetail:vms.colVcpu")}</TH><TH>{t("hostDetail:vms.colMemory")}</TH><TH>{t("hostDetail:vms.colAutostart")}</TH></tr>
      </THead>
      <TBody>
        {rows.map((v) => (
          <tr key={`${v.kind}-${v.external_id}`} className="hover:bg-panel-2">
            <TD className="text-fg-muted">{v.kind}</TD>
            <TD className="font-medium">{v.name}</TD>
            <TD className="text-fg-muted">{v.state}</TD>
            <TD className="tabular-nums text-fg-muted">{v.vcpu}</TD>
            <TD className="tabular-nums text-fg-muted">{formatBytes(v.mem_bytes)}</TD>
            <TD className="text-fg-muted">{v.autostart ? t("hostDetail:vms.yes") : t("hostDetail:vms.no")}</TD>
          </tr>
        ))}
      </TBody>
    </Table>
  );
}
