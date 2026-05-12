import { useQuery } from "@tanstack/react-query";
import { HardDrive } from "lucide-react";
import { useMemo, useState } from "react";

import type {
  ChartSeries} from "../../components/Chart";
import {
  ChartLine,
  colorFor,
  formatBytes,
  formatBytesPerSec,
  rateOfChange,
} from "../../components/Chart";
import {
  Empty,
  Panel,
  PanelBody,
  PanelHeader,
  PercentBar,
  Skeleton,
  TBody,
  TD,
  TH,
  THead,
  Table,
  TimeRangeSelector,
} from "../../components/ui";
import { useT } from "../../i18n/useT";
import { api } from "../../lib/api";
import type { DiskRow, DiskSample } from "../../lib/types";

import { useHostDetail } from "./HostLayout";

// Storage tab: per-mount disk I/O chart on top, snapshot of mount usage below.
export function Storage() {
  const { detail, hostId } = useHostDetail();
  const { t } = useT(["hostDetail", "common"]);
  const [rangeSec, setRangeSec] = useState(60 * 60);

  const fromTo = useMemo(() => {
    const to = new Date();
    const from = new Date(to.getTime() - rangeSec * 1000);
    return `from=${encodeURIComponent(from.toISOString())}&to=${encodeURIComponent(to.toISOString())}`;
  }, [rangeSec]);

  const disk = useQuery({
    queryKey: ["host-disk", hostId, rangeSec],
    queryFn: () => api<{ samples: DiskSample[] }>(`/v1/hosts/${hostId}/metrics/disk?${fromTo}`),
    refetchInterval: 30_000,
    enabled: !!hostId,
  });

  return (
    <div className="space-y-5">
      <div className="flex items-center justify-between">
        <h2 className="text-[11px] font-semibold uppercase tracking-wider text-fg-subtle">{t("hostDetail:storage.ioOverTime")}</h2>
        <TimeRangeSelector value={rangeSec} onChange={setRangeSec} />
      </div>
      <DiskIOPanel samples={disk.data?.samples ?? []} disks={detail.disks} loading={disk.isLoading} />
      <Panel>
        <PanelHeader><h3 className="text-sm font-semibold">{t("hostDetail:storage.mountsTitle", { count: detail.disks.length })}</h3></PanelHeader>
        <PanelBody className="p-0 overflow-x-auto"><DisksTable rows={detail.disks} /></PanelBody>
      </Panel>
    </div>
  );
}

// ---- Disks table --------------------------------------------------------

function DisksTable({ rows }: { rows: DiskRow[] }) {
  const { t } = useT(["hostDetail", "common"]);
  if (rows.length === 0) return <Empty>{t("hostDetail:storage.noDisks")}</Empty>;
  return (
    <Table>
      <THead>
        <tr><TH>{t("hostDetail:storage.colMount")}</TH><TH>{t("hostDetail:storage.colDevice")}</TH><TH>{t("hostDetail:storage.colFs")}</TH><TH>{t("hostDetail:storage.colSize")}</TH><TH>{t("hostDetail:storage.colUsed")}</TH><TH>{t("hostDetail:storage.colFree")}</TH><TH>{t("hostDetail:storage.colUse")}</TH></tr>
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

// ---- I/O chart ----------------------------------------------------------

function DiskIOPanel({ samples, disks, loading }: { samples: DiskSample[]; disks: DiskRow[]; loading: boolean }) {
  const { t } = useT(["hostDetail", "common"]);
  const { matrix, series } = useMemo(() => {
    if (samples.length === 0) return { matrix: [[]], series: [] as ChartSeries[] };
    const byMount = new Map<string, { times: number[]; read: number[]; write: number[] }>();
    for (const s of samples) {
      const ts = Math.floor(new Date(s.time).getTime() / 1000);
      const cur = byMount.get(s.mountpoint) ?? { times: [], read: [], write: [] };
      cur.times.push(ts);
      cur.read.push(s.read_bytes);
      cur.write.push(s.write_bytes);
      byMount.set(s.mountpoint, cur);
    }
    const timeSet = new Set<number>();
    byMount.forEach((v) => { v.times.forEach((ts) => timeSet.add(ts)); });
    const times = Array.from(timeSet).sort((a, b) => a - b);
    const seriesArr: ChartSeries[] = [];
    const cols: number[][] = [times];
    let i = 0;
    byMount.forEach((v, mount) => {
      cols.push(alignToAxis(times, v.times, rateOfChange(v.times, v.read)));
      cols.push(alignToAxis(times, v.times, rateOfChange(v.times, v.write)));
      seriesArr.push({ label: t("hostDetail:storage.seriesRead", { mount }), color: colorFor(i * 2) });
      seriesArr.push({ label: t("hostDetail:storage.seriesWrite", { mount }), color: colorFor(i * 2 + 1) });
      i++;
    });
    return { matrix: cols, series: seriesArr };
  }, [samples, t]);

  return (
    <Panel>
      <PanelHeader>
        <div className="flex items-center gap-2">
          <HardDrive className="h-4 w-4 text-fg-muted" />
          <h3 className="text-sm font-semibold">{t("hostDetail:storage.diskIoTitle")}</h3>
          <span className="ml-2 text-xs text-fg-subtle">{t("hostDetail:storage.diskIoSubtitle", { count: disks.length })}</span>
        </div>
      </PanelHeader>
      <PanelBody>
        {loading && samples.length === 0 ? (
          <Skeleton className="h-48" />
        ) : samples.length === 0 ? (
          <Empty>{t("hostDetail:storage.noSamples")}</Empty>
        ) : (
          <ChartLine data={{ matrix }} series={series} formatY={formatBytesPerSec} yMin={0} height={220} />
        )}
      </PanelBody>
    </Panel>
  );
}

// alignToAxis fills in zeros for any timestamp the source series didn't sample
// at. Used to plot multiple per-device series on a unified time axis without
// uPlot interpolating misleading values across gaps.
function alignToAxis(target: number[], src: number[], values: number[]): number[] {
  const out: number[] = new Array<number>(target.length).fill(0);
  let j = 0;
  for (let i = 0; i < target.length; i++) {
    while (j < src.length && src[j] < target[i]) j++;
    if (j < src.length && src[j] === target[i]) {
      out[i] = values[j];
    }
  }
  return out;
}
