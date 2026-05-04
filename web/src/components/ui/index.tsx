// Shared UI primitives. Keep them tiny and class-driven; Tailwind tokens are
// the single source of truth for colors/spacing/typography. No third-party
// component library is used — these compose directly on top of Tailwind.

import { ComponentPropsWithoutRef, KeyboardEvent, ReactNode, useRef } from "react";

// ---- Layout primitives -----------------------------------------------------

export function Panel({ className = "", children, ...rest }: ComponentPropsWithoutRef<"section">) {
  return (
    <section
      className={`rounded-lg border border-border bg-panel shadow-panel ${className}`}
      {...rest}
    >
      {children}
    </section>
  );
}

export function PanelHeader({ children }: { children: ReactNode }) {
  return (
    <header className="flex items-center justify-between border-b border-border px-5 py-3">
      {children}
    </header>
  );
}

export function PanelBody({ className = "", children }: { className?: string; children: ReactNode }) {
  return <div className={`p-5 ${className}`}>{children}</div>;
}

export function SectionHeading({ children }: { children: ReactNode }) {
  return (
    <h2 className="mb-2 text-[11px] font-semibold uppercase tracking-[0.08em] text-fg-subtle">
      {children}
    </h2>
  );
}

// ---- Status / severity -----------------------------------------------------

type StatusKey = "online" | "stale" | "offline" | "unknown" | "ok" | "warn" | "fail" | "info";

const STATUS_PALETTE: Record<StatusKey, string> = {
  online: "bg-ok/10 text-ok ring-1 ring-inset ring-ok/30",
  ok: "bg-ok/10 text-ok ring-1 ring-inset ring-ok/30",
  stale: "bg-warn/10 text-warn ring-1 ring-inset ring-warn/30",
  warn: "bg-warn/10 text-warn ring-1 ring-inset ring-warn/30",
  // TODO(theme): `bg-zinc-700/*` doesn't switch with the palette — replace
  // with a semantic token (e.g. `bg-offline/30`) in a follow-up.
  offline: "bg-zinc-700/30 text-fg-muted ring-1 ring-inset ring-border-strong",
  fail: "bg-fail/10 text-fail ring-1 ring-inset ring-fail/30",
  // TODO(theme): same as `offline` above — drop the raw zinc utility.
  unknown: "bg-zinc-700/20 text-fg-subtle ring-1 ring-inset ring-border",
  info: "bg-info/10 text-info ring-1 ring-inset ring-info/30",
};

export function StatusPill({ status, children }: { status: StatusKey | string; children?: ReactNode }) {
  const cls = STATUS_PALETTE[status as StatusKey] ?? STATUS_PALETTE.unknown;
  return (
    <span className={`inline-flex items-center gap-1 rounded-md px-1.5 py-0.5 text-[11px] font-medium ${cls}`}>
      {children ?? status}
    </span>
  );
}

export function Dot({ status, pulse = false }: { status: StatusKey | string; pulse?: boolean }) {
  const map: Record<StatusKey, string> = {
    online: "bg-ok",
    ok: "bg-ok",
    stale: "bg-warn",
    warn: "bg-warn",
    offline: "bg-fg-subtle",
    fail: "bg-fail",
    unknown: "bg-fg-subtle",
    info: "bg-info",
  };
  const cls = map[status as StatusKey] ?? map.unknown;
  const pulseCls = pulse && (status === "online" || status === "ok") ? "animate-pulse-soft" : "";
  return <span className={`inline-block h-2 w-2 rounded-full ${cls} ${pulseCls}`} />;
}

// ---- Stats ----------------------------------------------------------------

export function StatCard({ label, value, hint }: { label: string; value: ReactNode; hint?: ReactNode }) {
  return (
    <div className="rounded-lg border border-border bg-panel-2 p-4">
      <p className="text-[11px] font-medium uppercase tracking-wider text-fg-subtle">{label}</p>
      <p className="mt-1 text-xl font-semibold tabular-nums text-fg">{value}</p>
      {hint && <p className="mt-0.5 text-xs text-fg-muted tabular-nums">{hint}</p>}
    </div>
  );
}

// ---- Buttons --------------------------------------------------------------

type ButtonVariant = "primary" | "secondary" | "ghost" | "danger";

export function Button({
  variant = "secondary",
  className = "",
  children,
  ...rest
}: ComponentPropsWithoutRef<"button"> & { variant?: ButtonVariant }) {
  const base =
    "inline-flex items-center justify-center gap-1.5 rounded-md px-3 py-1.5 text-sm font-medium transition-colors duration-150 focus:outline-none disabled:opacity-50 disabled:cursor-not-allowed";
  const variants: Record<ButtonVariant, string> = {
    primary: "bg-accent text-bg hover:bg-accent-hover focus-visible:ring-2 focus-visible:ring-accent/60",
    secondary:
      "border border-border bg-panel text-fg hover:bg-panel-2 hover:border-border-strong focus-visible:ring-2 focus-visible:ring-accent/40",
    ghost: "text-fg-muted hover:text-fg hover:bg-panel-2 focus-visible:ring-2 focus-visible:ring-accent/40",
    danger:
      "border border-fail/30 bg-fail/10 text-fail hover:bg-fail/15 hover:border-fail/50 focus-visible:ring-2 focus-visible:ring-fail/40",
  };
  return (
    <button className={`${base} ${variants[variant]} ${className}`} {...rest}>
      {children}
    </button>
  );
}

// ---- Inputs ---------------------------------------------------------------

export function TextInput({ className = "", ...rest }: ComponentPropsWithoutRef<"input">) {
  return (
    <input
      className={`w-full rounded-md border border-border bg-panel px-3 py-2 text-sm text-fg placeholder:text-fg-subtle transition-colors duration-150 focus:border-accent focus:outline-none focus:ring-2 focus:ring-accent/30 ${className}`}
      {...rest}
    />
  );
}

export function Field({
  label,
  hint,
  children,
}: {
  label: string;
  hint?: ReactNode;
  children: ReactNode;
}) {
  return (
    <label className="block">
      <span className="mb-1 block text-xs font-medium text-fg-muted">{label}</span>
      {children}
      {hint && <span className="mt-1 block text-xs text-fg-subtle">{hint}</span>}
    </label>
  );
}

// ---- Empty / error / loading ---------------------------------------------

export function Empty({ children = "No data." }: { children?: ReactNode }) {
  return <p className="py-4 text-sm text-fg-subtle">{children}</p>;
}

export function ErrorBox({ children }: { children: ReactNode }) {
  return (
    <p className="rounded-md border border-fail/30 bg-fail/10 px-3 py-2 text-sm text-fail">
      {children}
    </p>
  );
}

export function SuccessBox({ children }: { children: ReactNode }) {
  return (
    <p className="rounded-md border border-ok/30 bg-ok/10 px-3 py-2 text-sm text-ok">
      {children}
    </p>
  );
}

// ---- Time-range selector --------------------------------------------------

// Shared by host detail charts. The range value is a duration in seconds —
// the consumer subtracts it from `now` to compute `from`.
export type RangeOption = { label: string; seconds: number };

export const DEFAULT_RANGES: RangeOption[] = [
  { label: "15m", seconds: 15 * 60 },
  { label: "1h", seconds: 60 * 60 },
  { label: "6h", seconds: 6 * 60 * 60 },
  { label: "24h", seconds: 24 * 60 * 60 },
  { label: "7d", seconds: 7 * 24 * 60 * 60 },
];

export function TimeRangeSelector({
  value,
  onChange,
  options = DEFAULT_RANGES,
}: {
  value: number;
  onChange: (seconds: number) => void;
  options?: RangeOption[];
}) {
  return (
    <div role="tablist" aria-label="Time range" className="inline-flex rounded-md border border-border bg-panel p-0.5">
      {options.map((opt) => {
        const active = opt.seconds === value;
        return (
          <button
            key={opt.label}
            role="tab"
            aria-selected={active}
            onClick={() => onChange(opt.seconds)}
            className={`rounded px-2.5 py-1 text-xs font-medium transition-colors duration-150 ${
              active ? "bg-panel-2 text-fg shadow-panel" : "text-fg-subtle hover:text-fg"
            }`}
          >
            {opt.label}
          </button>
        );
      })}
    </div>
  );
}

// ---- Tables ---------------------------------------------------------------

export function Table({ children }: { children: ReactNode }) {
  return <table className="w-full text-sm">{children}</table>;
}

export function THead({ children }: { children: ReactNode }) {
  return (
    <thead className="text-left text-[11px] font-semibold uppercase tracking-wider text-fg-subtle">
      {children}
    </thead>
  );
}

export function TH({ children, className = "" }: { children?: ReactNode; className?: string }) {
  return <th className={`px-3 py-2 font-semibold ${className}`}>{children}</th>;
}

export function TBody({ children }: { children: ReactNode }) {
  return <tbody className="divide-y divide-border">{children}</tbody>;
}

export function TD({ children, className = "" }: { children?: ReactNode; className?: string }) {
  return <td className={`px-3 py-2 ${className}`}>{children}</td>;
}

// ---- Misc helpers ---------------------------------------------------------

export function PercentBar({ pct }: { pct: number }) {
  const v = Math.max(0, Math.min(100, pct));
  const color = v > 90 ? "bg-fail" : v > 75 ? "bg-warn" : "bg-ok";
  return (
    <div className="flex items-center gap-2">
      <div className="h-1.5 w-24 overflow-hidden rounded-full bg-border">
        <div className={`h-full ${color} transition-all duration-200 ease-ui`} style={{ width: `${v}%` }} />
      </div>
      <span className="w-10 text-right text-xs tabular-nums text-fg-muted">{v.toFixed(0)}%</span>
    </div>
  );
}

// ---- Skeleton ------------------------------------------------------------

export function Skeleton({ className = "" }: { className?: string }) {
  return (
    <div
      className={`overflow-hidden rounded-md bg-panel-2 ${className}`}
      style={{
        backgroundImage:
          "linear-gradient(90deg, rgba(255,255,255,0) 0%, rgba(255,255,255,0.04) 50%, rgba(255,255,255,0) 100%)",
        backgroundSize: "800px 100%",
        animation: "shimmer 1.6s linear infinite",
      }}
      aria-hidden
    />
  );
}

// ---- Tabs ----------------------------------------------------------------

export type TabItem<T extends string> = {
  key: T;
  label: string;
  icon?: React.ComponentType<{ className?: string }>;
  badge?: ReactNode;
  hidden?: boolean;
};

export function Tabs<T extends string>({
  items,
  value,
  onChange,
  className = "",
  idPrefix = "tab",
  panelIdPrefix = "panel",
}: {
  items: ReadonlyArray<TabItem<T>>;
  value: T;
  onChange: (v: T) => void;
  className?: string;
  idPrefix?: string;
  panelIdPrefix?: string;
}) {
  const visibleItems = items.filter((i) => !i.hidden);
  const tablistRef = useRef<HTMLDivElement | null>(null);

  function onKeyDown(e: KeyboardEvent<HTMLDivElement>) {
    if (e.key !== "ArrowLeft" && e.key !== "ArrowRight") return;
    e.preventDefault();
    const idx = visibleItems.findIndex((i) => i.key === value);
    if (idx < 0 || visibleItems.length === 0) return;
    const delta = e.key === "ArrowRight" ? 1 : -1;
    const next = visibleItems[(idx + delta + visibleItems.length) % visibleItems.length];
    onChange(next.key);
    // Move focus to the newly active tab so arrow nav also moves DOM focus.
    const root = tablistRef.current;
    if (root) {
      const btn = root.querySelector<HTMLButtonElement>(`#${idPrefix}-${next.key}`);
      btn?.focus();
    }
  }

  return (
    <div
      ref={tablistRef}
      role="tablist"
      onKeyDown={onKeyDown}
      className={`sticky top-header-h z-20 -mx-2 flex gap-1 overflow-x-auto border-b border-border bg-bg/85 px-2 py-1.5 backdrop-blur supports-[backdrop-filter]:bg-bg/70 ${className}`}
    >
      {visibleItems.map(({ key, label, icon: Icon, badge }) => {
        const active = key === value;
        return (
          <button
            key={key}
            id={`${idPrefix}-${key}`}
            role="tab"
            aria-selected={active}
            aria-controls={`${panelIdPrefix}-${key}`}
            tabIndex={active ? 0 : -1}
            onClick={() => onChange(key)}
            className={`inline-flex shrink-0 items-center gap-1.5 rounded-md px-3 py-1.5 text-sm font-medium transition-colors duration-150 ${
              active
                ? "bg-panel-2 text-fg shadow-panel"
                : "text-fg-muted hover:bg-panel hover:text-fg"
            }`}
          >
            {Icon && <Icon className="h-3.5 w-3.5" />}
            {label}
            {badge !== undefined && badge !== null && (
              <span className="rounded-full bg-border-strong px-1.5 py-0.5 text-[10px] font-mono text-fg-muted">
                {badge}
              </span>
            )}
          </button>
        );
      })}
    </div>
  );
}

export function Kbd({ children }: { children: ReactNode }) {
  return (
    <kbd className="rounded border border-border bg-panel-2 px-1.5 py-0.5 font-mono text-[10px] text-fg-muted">
      {children}
    </kbd>
  );
}
