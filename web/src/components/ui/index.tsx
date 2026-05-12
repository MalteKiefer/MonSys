// Shared UI primitives. Keep them tiny and class-driven; Tailwind tokens are
// the single source of truth for colors/spacing/typography. No third-party
// component library is used — these compose directly on top of Tailwind.

export { Avatar } from "./Avatar";

import { Check } from "lucide-react";
import type {
  ComponentPropsWithoutRef,
  KeyboardEvent,
  ReactNode} from "react";
import {
  useEffect,
  useId,
  useMemo,
  useRef,
  useState,
} from "react";
import { useT } from "../../i18n/useT";

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
  offline: "bg-offline/20 text-fg-muted ring-1 ring-inset ring-border-strong",
  fail: "bg-fail/10 text-fail ring-1 ring-inset ring-fail/30",
  unknown: "bg-offline/10 text-fg-subtle ring-1 ring-inset ring-border",
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
type ButtonSize = "sm" | "md" | "lg";

// `size` controls the tap target.
//   sm  — h-7 (28px). Compact, used inside dense tables and inline toolbars.
//   md  — h-9 (36px) DEFAULT. Matches `--touch-min`; safe for touch.
//   lg  — h-11 (44px). Mobile-primary actions and full-width forms.
// Existing call sites pass no size, so keep "md" as the default. The visual
// height stays close to the prior fixed `py-1.5` (≈32px) — bumping the
// minimum to 36px is the small UX win; the rest of the geometry is the same.
const BUTTON_SIZES: Record<ButtonSize, string> = {
  sm: "min-h-7 px-2 py-1 text-xs",
  md: "min-h-9 px-3 py-1.5 text-sm",
  lg: "min-h-11 px-4 py-2 text-sm",
};

export function Button({
  variant = "secondary",
  size = "md",
  className = "",
  children,
  ...rest
}: ComponentPropsWithoutRef<"button"> & { variant?: ButtonVariant; size?: ButtonSize }) {
  const base =
    "inline-flex items-center justify-center gap-1.5 rounded-md font-medium transition-colors duration-150 focus:outline-none disabled:opacity-50 disabled:cursor-not-allowed";
  const variants: Record<ButtonVariant, string> = {
    primary: "bg-accent text-bg hover:bg-accent-hover focus-visible:ring-2 focus-visible:ring-accent/60",
    secondary:
      "border border-border bg-panel text-fg hover:bg-panel-2 hover:border-border-strong focus-visible:ring-2 focus-visible:ring-accent/40",
    ghost: "text-fg-muted hover:text-fg hover:bg-panel-2 focus-visible:ring-2 focus-visible:ring-accent/40",
    danger:
      "border border-fail/30 bg-fail/10 text-fail hover:bg-fail/15 hover:border-fail/50 focus-visible:ring-2 focus-visible:ring-fail/40",
  };
  return (
    <button className={`${base} ${BUTTON_SIZES[size]} ${variants[variant]} ${className}`} {...rest}>
      {children}
    </button>
  );
}

// ---- Inputs ---------------------------------------------------------------

export function TextInput({ className = "", ...rest }: ComponentPropsWithoutRef<"input">) {
  // `min-h-9` (36px) matches `--touch-min` so taps land reliably on mobile;
  // the existing `py-2 text-sm` keeps the desktop look identical.
  return (
    <input
      className={`w-full min-h-9 rounded-md border border-border bg-panel px-3 py-2 text-sm text-fg placeholder:text-fg-subtle transition-colors duration-150 focus:border-accent focus:outline-none focus:ring-2 focus:ring-accent/30 ${className}`}
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

export function Empty({ children }: { children?: ReactNode }) {
  const { t } = useT(["ui", "common"]);
  // Default text comes from i18n; callers can still override by passing
  // `children` (e.g. `<Empty>No NICs.</Empty>`).
  const content = children ?? t("ui:empty.default");
  return <p className="py-4 text-sm text-fg-subtle">{content}</p>;
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
export interface RangeOption { label: string; seconds: number }

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
  options,
}: {
  value: number;
  onChange: (seconds: number) => void;
  options?: RangeOption[];
}) {
  const { t } = useT(["ui", "common"]);
  // Build a localized default range list when the caller doesn't supply one.
  // Keys mirror the seconds-based DEFAULT_RANGES so existing call sites that
  // compare `seconds` against persisted values keep working unchanged.
  const localizedDefault = useMemo<RangeOption[]>(
    () => DEFAULT_RANGES.map((r) => ({ ...r, label: t(`ui:timeRange.labels.${r.label}` as const) })),
    [t],
  );
  const resolved = options ?? localizedDefault;
  return (
    <div role="tablist" aria-label={t("ui:timeRange.ariaLabel")} className="inline-flex rounded-md border border-border bg-panel p-0.5">
      {resolved.map((opt) => {
        const active = opt.seconds === value;
        return (
          <button
            key={opt.label}
            role="tab"
            aria-selected={active}
            onClick={() => { onChange(opt.seconds); }}
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

export interface TabItem<T extends string> {
  key: T;
  label: string;
  icon?: React.ComponentType<{ className?: string }>;
  badge?: ReactNode;
  hidden?: boolean;
}

export function Tabs<T extends string>({
  items,
  value,
  onChange,
  className = "",
  idPrefix = "tab",
  panelIdPrefix = "panel",
}: {
  items: readonly TabItem<T>[];
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
            onClick={() => { onChange(key); }}
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

// ---- Stepper -------------------------------------------------------------

// Generic horizontal stepper. Each step is a button with a circle (number,
// check for completed, accent fill for current) and a label. An optional
// description shows under the label of the active step only. When `onJump`
// is supplied, completed steps can be clicked and Arrow Left/Right cycles
// focus; future steps are disabled. Without `onJump` the row is purely
// presentational — the buttons render disabled but no click handler runs.
export interface StepperItem { key: string; label: string; description?: string }

export function Stepper({
  items,
  current,
  completed,
  onJump,
  className = "",
}: {
  items: readonly StepperItem[];
  current: number;
  completed?: readonly number[];
  onJump?: (idx: number) => void;
  className?: string;
}) {
  const { t } = useT(["ui", "common"]);
  const listRef = useRef<HTMLOListElement | null>(null);
  const completedSet = new Set(completed ?? []);

  function isCompleted(idx: number): boolean {
    return completedSet.has(idx);
  }

  function canJumpTo(idx: number): boolean {
    if (!onJump) return false;
    if (idx === current) return false;
    // Always allow jumping to a past or completed step. Future steps are
    // only reachable if they are explicitly marked completed (e.g. when the
    // caller has validated them ahead of time).
    return idx < current || isCompleted(idx);
  }

  function focusStep(idx: number) {
    const root = listRef.current;
    if (!root) return;
    const btn = root.querySelector<HTMLButtonElement>(`[data-step-idx="${idx}"]`);
    btn?.focus();
  }

  function onKeyDown(e: KeyboardEvent<HTMLOListElement>) {
    if (!onJump) return;
    if (e.key !== "ArrowLeft" && e.key !== "ArrowRight") return;
    e.preventDefault();
    const delta = e.key === "ArrowRight" ? 1 : -1;
    // Clamp at the boundaries instead of wrapping. Wrapping was silently
    // swallowed if the wrap-target wasn't clickable, leaving the user with
    // no feedback — clamping makes the boundary a no-op the user can feel.
    if (delta === -1 && current === 0) return;
    if (delta === 1 && current === items.length - 1) return;
    const next = current + delta;
    if (canJumpTo(next)) {
      onJump(next);
      focusStep(next);
    } else {
      // Even when we can't jump (next step is disabled), still move focus
      // so AT announces "step X, disabled" and the user understands why
      // their arrow press didn't navigate.
      focusStep(next);
    }
  }

  return (
    <ol
      ref={listRef}
      onKeyDown={onKeyDown}
      className={`flex w-full items-start gap-2 ${className}`}
      aria-label={t("ui:stepper.ariaLabel")}
    >
      {items.map((item, idx) => {
        const isCurrent = idx === current;
        const isDone = isCompleted(idx) || idx < current;
        const clickable = canJumpTo(idx);
        const circleCls = isDone
          ? "bg-ok/20 text-ok ring-ok/40"
          : isCurrent
            ? "bg-accent/20 text-accent ring-accent/50"
            : "bg-panel-2 text-fg-subtle ring-border";
        const labelCls = isCurrent
          ? "text-fg"
          : isDone
            ? "text-fg-muted"
            : "text-fg-subtle";
        return (
          <li key={item.key} className="flex flex-1 items-start gap-2">
            <button
              type="button"
              data-step-idx={idx}
              // Use aria-disabled so the button stays focusable for keyboard
              // arrow navigation — AT announces "disabled" while we still
              // suppress activation in the onClick handler.
              aria-disabled={!clickable && !isCurrent ? true : undefined}
              tabIndex={isCurrent ? 0 : -1}
              onClick={() => {
                if (clickable && onJump) onJump(idx);
              }}
              className={`group flex flex-col items-start gap-1 rounded-md px-1 py-0.5 text-left transition-colors duration-150 focus:outline-none focus:ring-2 focus:ring-accent/40 ${
                clickable ? "cursor-pointer" : "cursor-default"
              } ${!clickable && !isCurrent ? "opacity-60" : ""}`}
              aria-current={isCurrent ? "step" : undefined}
            >
              <span className="flex items-center gap-2">
                <span
                  aria-hidden
                  className={`flex h-6 w-6 shrink-0 items-center justify-center rounded-full text-[11px] font-semibold ring-1 ring-inset transition-colors duration-150 ${circleCls}`}
                >
                  {isDone ? <Check className="h-3.5 w-3.5" /> : idx + 1}
                </span>
                <span className={`text-xs font-medium ${labelCls}`}>{item.label}</span>
              </span>
              {isCurrent && item.description && (
                <span className="pl-8 text-[11px] leading-snug text-fg-subtle">
                  {item.description}
                </span>
              )}
            </button>
            {idx < items.length - 1 && (
              <span
                aria-hidden
                className={`mt-3 h-px flex-1 ${isDone ? "bg-ok/50" : "bg-border"}`}
              />
            )}
          </li>
        );
      })}
    </ol>
  );
}

// ---- DropdownMenu --------------------------------------------------------

// Tiny popover menu used for row-action kebabs. Click trigger to toggle,
// Escape / outside-click to close, ArrowUp/Down for roving focus inside the
// menu. Items can be disabled (with a hover tooltip via `disabledReason`)
// and/or destructive (rendered in `text-fail`). The trigger is wrapped in a
// span so consumers can pass either a styled <button> or a plain icon — the
// keyboard handler is attached to the wrapper.

export interface DropdownItem {
  key: string;
  label: string;
  icon?: React.ComponentType<{ className?: string }>;
  onClick: () => void;
  destructive?: boolean;
  disabled?: boolean;
  // When set, shows as a native title tooltip on the disabled item so the
  // user can discover *why* the action is unavailable.
  disabledReason?: string;
}

export function DropdownMenu({
  trigger,
  items,
  align = "right",
}: {
  trigger: ReactNode;
  items: readonly DropdownItem[];
  align?: "left" | "right";
}) {
  const [open, setOpen] = useState(false);
  const [activeIdx, setActiveIdx] = useState<number>(-1);
  const wrapRef = useRef<HTMLDivElement | null>(null);
  const itemRefs = useRef<(HTMLButtonElement | null)[]>([]);
  const menuId = useId();

  // Build a sparse refs array sized to the current items list so roving
  // focus indices stay aligned across renders.
  itemRefs.current = itemRefs.current.slice(0, items.length);

  // Track the previous open state so we can detect the true→false transition
  // and restore focus to the trigger on close — regardless of whether the
  // close was triggered by Escape, outside-click, or item activation.
  const prevOpenRef = useRef(open);
  useEffect(() => {
    if (prevOpenRef.current && !open) {
      const trig = wrapRef.current?.querySelector<HTMLElement>(
        "[data-dropdown-trigger]",
      );
      trig?.focus();
    }
    prevOpenRef.current = open;
  }, [open]);

  useEffect(() => {
    if (!open) return;
    function onDocPointer(e: MouseEvent | TouchEvent) {
      const root = wrapRef.current;
      if (!root) return;
      if (e.target instanceof Node && root.contains(e.target)) return;
      setOpen(false);
    }
    function onKey(e: globalThis.KeyboardEvent) {
      if (e.key === "Escape") {
        e.preventDefault();
        setOpen(false);
      }
    }
    document.addEventListener("mousedown", onDocPointer);
    document.addEventListener("touchstart", onDocPointer);
    document.addEventListener("keydown", onKey);
    return () => {
      document.removeEventListener("mousedown", onDocPointer);
      document.removeEventListener("touchstart", onDocPointer);
      document.removeEventListener("keydown", onKey);
    };
  }, [open]);

  // Whenever we open, move focus to the first non-disabled item so screen
  // readers and keyboard users land somewhere actionable.
  useEffect(() => {
    if (!open) {
      setActiveIdx(-1);
      return;
    }
    const firstEnabled = items.findIndex((it) => !it.disabled);
    setActiveIdx(firstEnabled);
  }, [open, items]);

  useEffect(() => {
    if (!open || activeIdx < 0) return;
    itemRefs.current[activeIdx]?.focus();
  }, [open, activeIdx]);

  function moveFocus(delta: 1 | -1) {
    if (items.length === 0) return;
    let idx = activeIdx;
    for (let i = 0; i < items.length; i++) {
      idx = (idx + delta + items.length) % items.length;
      if (!items[idx].disabled) {
        setActiveIdx(idx);
        return;
      }
    }
  }

  function onMenuKeyDown(e: KeyboardEvent<HTMLDivElement>) {
    if (e.key === "ArrowDown") {
      e.preventDefault();
      moveFocus(1);
    } else if (e.key === "ArrowUp") {
      e.preventDefault();
      moveFocus(-1);
    } else if (e.key === "Home") {
      e.preventDefault();
      const first = items.findIndex((it) => !it.disabled);
      if (first >= 0) setActiveIdx(first);
    } else if (e.key === "End") {
      e.preventDefault();
      for (let i = items.length - 1; i >= 0; i--) {
        if (!items[i].disabled) {
          setActiveIdx(i);
          return;
        }
      }
    } else if (e.key === "Tab") {
      // Tab out closes the menu so we don't trap focus inside.
      setOpen(false);
    }
  }

  function onTriggerKeyDown(e: KeyboardEvent<HTMLDivElement>) {
    if (e.key === "ArrowDown" || e.key === "Enter" || e.key === " ") {
      if (!open) {
        e.preventDefault();
        setOpen(true);
      }
    }
  }

  const alignCls = align === "left" ? "left-0" : "right-0";

  return (
    <div
      ref={wrapRef}
      className="relative inline-block"
      onKeyDown={onTriggerKeyDown}
    >
      <div
        data-dropdown-trigger
        onClick={() => { setOpen((v) => !v); }}
        aria-haspopup="menu"
        aria-expanded={open}
        aria-controls={open ? menuId : undefined}
        className="inline-flex"
      >
        {trigger}
      </div>
      {open && (
        <div
          id={menuId}
          role="menu"
          onKeyDown={onMenuKeyDown}
          className={`absolute z-30 mt-1 min-w-[12rem] overflow-hidden rounded-md border border-border bg-panel shadow-panel ${alignCls}`}
        >
          {items.map((it, i) => {
            const Icon = it.icon;
            const base =
              "flex w-full items-center gap-2 px-3 py-2 text-left text-sm transition-colors duration-150 focus:outline-none";
            const enabled = it.destructive
              ? "text-fail hover:bg-fail/10 focus-visible:bg-fail/10"
              : "text-fg hover:bg-panel-2 focus-visible:bg-panel-2";
            const disabledCls = "cursor-not-allowed text-fg-subtle opacity-60";
            // When the item is disabled with a reason, render a visually
            // hidden description that AT can pick up via aria-describedby —
            // touch users and SR users get the same information that sighted
            // pointer users get from the native `title=` tooltip.
            const reasonId =
              it.disabled && it.disabledReason
                ? `${menuId}-reason-${it.key}`
                : undefined;
            return (
              <button
                key={it.key}
                ref={(el) => {
                  itemRefs.current[i] = el;
                }}
                role="menuitem"
                aria-disabled={it.disabled || undefined}
                aria-describedby={reasonId}
                title={it.disabled ? it.disabledReason : undefined}
                tabIndex={i === activeIdx ? 0 : -1}
                onClick={() => {
                  if (it.disabled) return;
                  setOpen(false);
                  it.onClick();
                }}
                className={`${base} ${it.disabled ? disabledCls : enabled}`}
              >
                {Icon && <Icon className="h-3.5 w-3.5 shrink-0" />}
                <span className="flex-1 truncate">{it.label}</span>
                {reasonId && (
                  <span id={reasonId} className="sr-only">
                    {it.disabledReason}
                  </span>
                )}
              </button>
            );
          })}
        </div>
      )}
    </div>
  );
}
