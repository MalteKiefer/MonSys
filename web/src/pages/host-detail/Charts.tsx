import { useQuery } from "@tanstack/react-query";
import { Activity, Cpu, MemoryStick } from "lucide-react";
import { useMemo, useState } from "react";

import type {
  ChartSeries} from "../../components/Chart";
import {
  ChartLine,
  colorFor,
  formatBytes,
  formatPercent,
} from "../../components/Chart";
import {
  Empty,
  Panel,
  PanelBody,
  PanelHeader,
  Skeleton,
  TimeRangeSelector,
} from "../../components/ui";
import { useT } from "../../i18n/useT";
import { api } from "../../lib/api";
import type { SystemSample } from "../../lib/types";

import { useHostDetail } from "./HostLayout";

// Charts tab: dedicated long-form view of CPU/memory/load. Overview already
// renders a combined CPU+RAM chart for at-a-glance use; this tab breaks the
// signals out so each metric gets its own y-axis and isn't squashed by
// neighbouring series.
export function Charts() {
  const { detail, hostId } = useHostDetail();
  const { t } = useT(["hostDetail", "common"]);
  const [rangeSec, setRangeSec] = useState(60 * 60);

  const fromTo = useMemo(() => {
    const to = new Date();
    const from = new Date(to.getTime() - rangeSec * 1000);
    return `from=${encodeURIComponent(from.toISOString())}&to=${encodeURIComponent(to.toISOString())}`;
  }, [rangeSec]);

  const sys = useQuery({
    queryKey: ["host-system", hostId, rangeSec],
    queryFn: () => api<{ samples: SystemSample[] }>(`/v1/hosts/${hostId}/metrics/system?${fromTo}`),
    refetchInterval: 30_000,
    enabled: !!hostId,
  });

  const samples = useMemo(() => sys.data?.samples ?? [], [sys.data]);
  const ramTotal = detail.host.ram_total_bytes;

  // Pre-compute the time axis once and reuse it across all three charts so
  // they share an x scale visually (uPlot itself doesn't link them, but
  // tooltips line up which is what users actually compare).
  const times = useMemo(
    () => samples.map((s) => Math.floor(new Date(s.time).getTime() / 1000)),
    [samples],
  );

  const cpuMatrix = useMemo(() => [times, samples.map((s) => s.cpu_usage_pct)], [times, samples]);
  const ramMatrix = useMemo(
    () => [times, samples.map((s) => (ramTotal > 0 ? (s.ram_used_bytes / ramTotal) * 100 : 0))],
    [times, samples, ramTotal],
  );
  const loadMatrix = useMemo(
    () => [
      times,
      samples.map((s) => s.load_1),
      samples.map((s) => s.load_5),
      samples.map((s) => s.load_15),
    ],
    [times, samples],
  );

  return (
    <div className="space-y-5">
      <div className="flex items-center justify-between">
        <h2 className="text-[11px] font-semibold uppercase tracking-wider text-fg-subtle">{t("hostDetail:charts.systemCharts")}</h2>
        <TimeRangeSelector value={rangeSec} onChange={setRangeSec} />
      </div>

      <ChartPanel title={t("hostDetail:charts.cpuUsage")} icon={Cpu} loading={sys.isLoading} empty={samples.length === 0}>
        <ChartLine
          data={{ matrix: cpuMatrix }}
          series={[{ label: t("hostDetail:charts.cpuSeries"), color: colorFor(0), fill: "rgba(16,185,129,0.10)" }]}
          formatY={formatPercent}
          yMin={0}
          height={220}
        />
      </ChartPanel>

      <ChartPanel
        title={t("hostDetail:charts.memoryUsage")}
        icon={MemoryStick}
        loading={sys.isLoading}
        empty={samples.length === 0}
        sub={ramTotal > 0 ? t("hostDetail:charts.totalSub", { total: formatBytes(ramTotal) }) : undefined}
      >
        <ChartLine
          data={{ matrix: ramMatrix }}
          series={[{ label: t("hostDetail:charts.ramSeries"), color: colorFor(1), fill: "rgba(96,165,250,0.10)" }]}
          formatY={formatPercent}
          yMin={0}
          height={220}
        />
      </ChartPanel>

      <ChartPanel title={t("hostDetail:charts.loadAverage")} icon={Activity} loading={sys.isLoading} empty={samples.length === 0}>
        <ChartLine
          data={{ matrix: loadMatrix }}
          series={
            [
              { label: t("hostDetail:charts.load1"), color: colorFor(0) },
              { label: t("hostDetail:charts.load5"), color: colorFor(1) },
              { label: t("hostDetail:charts.load15"), color: colorFor(2) },
            ] as ChartSeries[]
          }
          yMin={0}
          height={220}
        />
      </ChartPanel>
    </div>
  );
}

function ChartPanel({
  title,
  icon: Icon,
  sub,
  loading,
  empty,
  children,
}: {
  title: string;
  icon: React.ComponentType<{ className?: string }>;
  sub?: string;
  loading: boolean;
  empty: boolean;
  children: React.ReactNode;
}) {
  const { t } = useT(["hostDetail", "common"]);
  return (
    <Panel>
      <PanelHeader>
        <div className="flex items-center gap-2">
          <Icon className="h-4 w-4 text-fg-muted" />
          <h3 className="text-sm font-semibold">{title}</h3>
          {sub && <span className="ml-2 text-xs text-fg-subtle">{sub}</span>}
        </div>
      </PanelHeader>
      <PanelBody>
        {loading && empty ? (
          <Skeleton className="h-48" />
        ) : empty ? (
          <Empty>{t("hostDetail:charts.noSamples")}</Empty>
        ) : (
          children
        )}
      </PanelBody>
    </Panel>
  );
}
