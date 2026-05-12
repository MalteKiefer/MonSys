import { useQuery } from "@tanstack/react-query";
import { GitMerge, Network as NetworkIcon } from "lucide-react";
import { useMemo, useState } from "react";

import {
  ChartLine,
  ChartSeries,
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
import { NetSample, NicRow } from "../../lib/types";

import { useHostDetail } from "./HostLayout";

// Network tab: throughput chart + interface inventory table.
export function Network() {
  const { detail, hostId } = useHostDetail();
  const { t } = useT(["hostDetail", "common"]);
  const [rangeSec, setRangeSec] = useState(60 * 60);

  const fromTo = useMemo(() => {
    const to = new Date();
    const from = new Date(to.getTime() - rangeSec * 1000);
    return `from=${encodeURIComponent(from.toISOString())}&to=${encodeURIComponent(to.toISOString())}`;
  }, [rangeSec]);

  const net = useQuery({
    queryKey: ["host-net", hostId, rangeSec],
    queryFn: () => api<{ samples: NetSample[] }>(`/v1/hosts/${hostId}/metrics/net?${fromTo}`),
    refetchInterval: 30_000,
    enabled: !!hostId,
  });

  return (
    <div className="space-y-5">
      <div className="flex items-center justify-between">
        <h2 className="text-[11px] font-semibold uppercase tracking-wider text-fg-subtle">{t("hostDetail:network.throughputOverTime")}</h2>
        <TimeRangeSelector value={rangeSec} onChange={setRangeSec} />
      </div>
      <NetIOPanel samples={net.data?.samples ?? []} nics={detail.nics} loading={net.isLoading} />
      <Panel>
        <PanelHeader><h3 className="text-sm font-semibold">{t("hostDetail:network.interfacesTitle", { count: detail.nics.length })}</h3></PanelHeader>
        <PanelBody className="p-0 overflow-x-auto"><NicsTable rows={detail.nics} /></PanelBody>
      </Panel>
    </div>
  );
}

// ---- NICs table ---------------------------------------------------------

function NicsTable({ rows }: { rows: NicRow[] }) {
  const { t } = useT(["hostDetail", "common"]);
  if (rows.length === 0) return <Empty>{t("hostDetail:network.noNics")}</Empty>;
  return (
    <Table>
      <THead>
        <tr>
          <TH>{t("hostDetail:network.colName")}</TH>
          <TH>{t("hostDetail:network.colMac")}</TH>
          <TH>{t("hostDetail:network.colAddresses")}</TH>
          <TH>{t("hostDetail:network.colMaster")}</TH>
          <TH>{t("hostDetail:network.colSpeed")}</TH>
          <TH>{t("hostDetail:network.colRxTotal")}</TH>
          <TH>{t("hostDetail:network.colTxTotal")}</TH>
        </tr>
      </THead>
      <TBody>
        {rows.map((n) => {
          const addrs = (n.addrs ?? []).filter(Boolean);
          const v4 = addrs.filter((a) => !a.includes(":"));
          const v6 = addrs.filter((a) => a.includes(":"));
          const members = (n.members ?? []).filter(Boolean);
          const isBridge = members.length > 0;
          return (
            <tr key={n.id} className="hover:bg-panel-2 align-top">
              <TD className="font-mono text-xs">
                <span className="inline-flex items-center gap-1.5">
                  {isBridge && <GitMerge className="h-3.5 w-3.5 text-fg-muted" aria-label={t("hostDetail:network.bridgeOrBondAria")} />}
                  {n.name}
                </span>
              </TD>
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
              <TD className="font-mono text-xs">
                {isBridge ? (
                  <span title={t("hostDetail:network.bridgeOrBondTitle")}>{t("hostDetail:network.bridgePrefix", { members: members.join(", ") })}</span>
                ) : n.bridge_master ? (
                  <span className="text-fg-muted" title={t("hostDetail:network.enslavedToTitle", { master: n.bridge_master })}>{t("hostDetail:network.enslavedTo", { master: n.bridge_master })}</span>
                ) : (
                  <span className="text-fg-subtle">—</span>
                )}
              </TD>
              <TD className="text-fg-muted">{n.speed_mbps ? t("hostDetail:network.speedMbps", { speed: n.speed_mbps }) : "—"}</TD>
              <TD className="tabular-nums">{formatBytes(n.rx_bytes)}</TD>
              <TD className="tabular-nums">{formatBytes(n.tx_bytes)}</TD>
            </tr>
          );
        })}
      </TBody>
    </Table>
  );
}

// ---- Throughput chart ---------------------------------------------------

function NetIOPanel({ samples, nics, loading }: { samples: NetSample[]; nics: NicRow[]; loading: boolean }) {
  const { t } = useT(["hostDetail", "common"]);
  const { matrix, series } = useMemo(() => {
    if (samples.length === 0) return { matrix: [[]], series: [] as ChartSeries[] };
    const byNic = new Map<string, { times: number[]; rx: number[]; tx: number[] }>();
    for (const s of samples) {
      const ts = Math.floor(new Date(s.time).getTime() / 1000);
      const cur = byNic.get(s.nic_name) ?? { times: [], rx: [], tx: [] };
      cur.times.push(ts);
      cur.rx.push(s.rx_bytes);
      cur.tx.push(s.tx_bytes);
      byNic.set(s.nic_name, cur);
    }
    const timeSet = new Set<number>();
    byNic.forEach((v) => v.times.forEach((ts) => timeSet.add(ts)));
    const times = Array.from(timeSet).sort((a, b) => a - b);
    const cols: number[][] = [times];
    const seriesArr: ChartSeries[] = [];
    let i = 0;
    byNic.forEach((v, name) => {
      cols.push(alignToAxis(times, v.times, rateOfChange(v.times, v.rx)));
      cols.push(alignToAxis(times, v.times, rateOfChange(v.times, v.tx)));
      seriesArr.push({ label: t("hostDetail:network.seriesRx", { name }), color: colorFor(i * 2) });
      seriesArr.push({ label: t("hostDetail:network.seriesTx", { name }), color: colorFor(i * 2 + 1) });
      i++;
    });
    return { matrix: cols, series: seriesArr };
  }, [samples, t]);

  return (
    <Panel>
      <PanelHeader>
        <div className="flex items-center gap-2">
          <NetworkIcon className="h-4 w-4 text-fg-muted" />
          <h3 className="text-sm font-semibold">{t("hostDetail:network.networkIoTitle")}</h3>
          <span className="ml-2 text-xs text-fg-subtle">{t("hostDetail:network.networkIoSubtitle", { count: nics.length })}</span>
        </div>
      </PanelHeader>
      <PanelBody>
        {loading && samples.length === 0 ? (
          <Skeleton className="h-48" />
        ) : samples.length === 0 ? (
          <Empty>{t("hostDetail:network.noSamples")}</Empty>
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
