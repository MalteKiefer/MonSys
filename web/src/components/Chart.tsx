// Tiny uPlot wrapper for time-series. The exposed API is a single `data`
// matrix [timestamps, ...series] and `series` describing each line. uPlot is
// fast even for thousands of points, has zero dependencies, and respects
// responsive sizing via ResizeObserver.
//
// The pure helper functions (formatBytes, colorFor, etc.) and the type-only
// declarations (ChartSeries, ChartData) live in `chart-utils.ts`. Importing
// just those from Chart.tsx used to drag uPlot's runtime + CSS into every
// caller — including transitive callers like the login bundle. They're
// re-exported here so existing consumers keep working, but the bundler is
// now free to tree-shake: callers that only need `formatBytes` end up with
// the uplot-free `chart-utils` chunk, while only direct `ChartLine` users
// pull `vendor-charts`.

import { useEffect, useRef } from "react";
import type { Options } from "uplot";
import uPlot from "uplot";
// uPlot's stylesheet is colocated with the runtime — keep it scoped to this
// module so the main `index.css` doesn't bake it in. When `ChartLine` is
// lazy-rendered the CSS now ships in the chart vendor chunk instead of the
// render-blocking app stylesheet.
import "uplot/dist/uPlot.min.css";

import type { ChartData, ChartSeries } from "./chart-utils";

// Re-export the helpers so existing `import { formatBytes } from
// "../components/Chart"` callers stay valid without code churn. Once all
// callers are migrated to `./chart-utils` these can be removed.
export type { ChartSeries, ChartData } from "./chart-utils";
export {
  formatBytesPerSec,
  formatBytes,
  formatPercent,
  SERIES_COLORS,
  colorFor,
  rateOfChange,
} from "./chart-utils";

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
