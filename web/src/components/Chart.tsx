// Tiny uPlot wrapper for time-series. The exposed API is a single `data`
// matrix [timestamps, ...series] and `series` describing each line. uPlot is
// fast even for thousands of points, has zero dependencies, and respects
// responsive sizing via ResizeObserver.

import { useEffect, useRef } from "react";
import type { Options } from "uplot";
import uPlot from "uplot";

export interface ChartSeries {
  label: string;
  // Stroke color. Tailwind tokens aren't available here, so we use raw hex
  // matching theme colors in tailwind.config.js.
  color: string;
  // Optional: filled area below the line, e.g. "rgba(16,185,129,0.10)".
  fill?: string;
}

export interface ChartData {
  // First entry is unix-second timestamps; subsequent are y-values per series.
  // Length: 1 + series.length.
  matrix: number[][];
}

export function ChartLine({
  data,
  series,
  height = 200,
  formatY,
  yMin,
}: {
  data: ChartData;
  series: ChartSeries[];
  height?: number;
  formatY?: (v: number) => string;
  yMin?: number;
}) {
  const containerRef = useRef<HTMLDivElement>(null);
  const plotRef = useRef<uPlot | null>(null);

  useEffect(() => {
    if (!containerRef.current) return;

    const opts: Options = {
      width: containerRef.current.clientWidth,
      height,
      legend: { show: true, live: true },
      scales: {
        x: { time: true },
        y: { auto: true, range: yMin !== undefined ? (_u, _min, max) => [yMin, max] : undefined },
      },
      axes: [
        {
          stroke: "#a1a1aa",
          grid: { stroke: "#27272a", width: 1 },
          ticks: { stroke: "#27272a" },
        },
        {
          stroke: "#a1a1aa",
          grid: { stroke: "#27272a", width: 1 },
          ticks: { stroke: "#27272a" },
          values: formatY ? (_u, vals) => vals.map(formatY) : undefined,
        },
      ],
      series: [
        {},
        ...series.map((s) => ({
          label: s.label,
          stroke: s.color,
          width: 1.5,
          fill: s.fill,
          points: { show: false },
          value: formatY ? (_u: uPlot, v: number | null) => (v == null ? "—" : formatY(v)) : undefined,
        })),
      ],
      cursor: {
        drag: { x: true, y: false },
        focus: { prox: 24 },
      },
    };

    plotRef.current = new uPlot(opts, data.matrix as uPlot.AlignedData, containerRef.current);

    const ro = new ResizeObserver(() => {
      if (containerRef.current && plotRef.current) {
        plotRef.current.setSize({ width: containerRef.current.clientWidth, height });
      }
    });
    ro.observe(containerRef.current);

    return () => {
      ro.disconnect();
      plotRef.current?.destroy();
      plotRef.current = null;
    };
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [height]);

  // Update data on prop change without rebuilding the chart — keeps zoom +
  // cursor state across refreshes.
  useEffect(() => {
    if (plotRef.current) {
      plotRef.current.setData(data.matrix as uPlot.AlignedData);
    }
  }, [data]);

  return <div ref={containerRef} className="w-full" />;
}

// ---- Helpers --------------------------------------------------------------

export function formatBytesPerSec(v: number): string {
  if (!Number.isFinite(v) || v <= 0) return "0 B/s";
  const units = ["B/s", "KiB/s", "MiB/s", "GiB/s"];
  let n = v;
  let i = 0;
  while (n >= 1024 && i < units.length - 1) {
    n /= 1024;
    i++;
  }
  return `${n.toFixed(n < 10 && i > 0 ? 1 : 0)} ${units[i]}`;
}

export function formatBytes(v: number): string {
  if (!Number.isFinite(v) || v <= 0) return "0";
  const units = ["B", "KiB", "MiB", "GiB", "TiB"];
  let n = v;
  let i = 0;
  while (n >= 1024 && i < units.length - 1) {
    n /= 1024;
    i++;
  }
  return `${n.toFixed(n < 10 && i > 0 ? 1 : 0)} ${units[i]}`;
}

export function formatPercent(v: number): string {
  return `${v.toFixed(1)} %`;
}

// Stable-ish color cycle for series. Hand-picked for dark backgrounds.
export const SERIES_COLORS = [
  "#10b981", // emerald (accent)
  "#60a5fa", // blue
  "#a78bfa", // violet
  "#f59e0b", // amber
  "#f472b6", // pink
  "#34d399", // lighter emerald
  "#fbbf24", // bright amber
  "#22d3ee", // cyan
  "#fb7185", // rose
];

export function colorFor(idx: number): string {
  return SERIES_COLORS[idx % SERIES_COLORS.length];
}

// rateOfChange converts cumulative counters (e.g. cumulative rx_bytes) into
// a per-second rate aligned to `times`. The first sample becomes 0 since we
// can't infer a delta. Negative values (counter wraparound) clamp to 0.
export function rateOfChange(times: number[], values: number[]): number[] {
  const out: number[] = new Array<number>(times.length).fill(0);
  for (let i = 1; i < times.length; i++) {
    const dt = times[i] - times[i - 1];
    if (dt <= 0) continue;
    const dv = values[i] - values[i - 1];
    out[i] = dv > 0 ? dv / dt : 0;
  }
  return out;
}
