// Pure helper functions extracted from Chart.tsx so consumers that only need
// formatters (formatBytes, colorFor, etc.) don't transitively import uPlot.
// The previous arrangement bundled uPlot into anything that imported
// `formatBytes`, which is why vendor-charts was being modulepreloaded even
// on the login page. By keeping these helpers in their own module, only
// pages that render <ChartLine /> pull the uPlot vendor chunk.

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
