import { useQuery } from "@tanstack/react-query";
import { Network as NetworkIcon } from "lucide-react";
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
import { api } from "../../lib/api";
import { NetSample, NicRow } from "../../lib/types";

import { useHostDetail } from "./HostLayout";

// Network tab: throughput chart + interface inventory table.
export function Network() {
  const { detail, hostId } = useHostDetail();
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
        <h2 className="text-[11px] font-semibold uppercase tracking-wider text-fg-subtle">Throughput over time</h2>
        <TimeRangeSelector value={rangeSec} onChange={setRangeSec} />
      </div>
      <NetIOPanel samples={net.data?.samples ?? []} nics={detail.nics} loading={net.isLoading} />
      <Panel>
        <PanelHeader><h3 className="text-sm font-semibold">Interfaces ({detail.nics.length})</h3></PanelHeader>
        <PanelBody className="p-0 overflow-x-auto"><NicsTable rows={detail.nics} /></PanelBody>
      </Panel>
    </div>
  );
}

// ---- NICs table ---------------------------------------------------------

function NicsTable({ rows }: { rows: NicRow[] }) {
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

// ---- Throughput chart ---------------------------------------------------

function NetIOPanel({ samples, nics, loading }: { samples: NetSample[]; nics: NicRow[]; loading: boolean }) {
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
          <NetworkIcon className="h-4 w-4 text-fg-muted" />
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
