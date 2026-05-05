// Sub-components used by RuleForm and ChannelForm in
// pages/AdminNotifications.tsx. Kept here to keep the page file readable.
//
// All visuals are Tailwind + UI primitives. No new behaviour — these are
// presentational pieces driven entirely by their props.

import { ReactNode } from "react";
import { LucideIcon } from "lucide-react";

// ---- Section ---------------------------------------------------------------

// Compact form section with an uppercase label and a thin bottom divider.
// Mirrors the `SectionHeading` typographic scale used across the app.
export function FormSection({
  label,
  hint,
  children,
  divider = true,
}: {
  label: string;
  hint?: ReactNode;
  children: ReactNode;
  divider?: boolean;
}) {
  return (
    <section className={divider ? "border-b border-border pb-5" : ""}>
      <div className="mb-3 flex items-baseline justify-between gap-3">
        <h4 className="text-[11px] font-semibold uppercase tracking-[0.08em] text-fg-subtle">
          {label}
        </h4>
        {hint && <span className="text-[11px] text-fg-subtle">{hint}</span>}
      </div>
      <div className="space-y-3">{children}</div>
    </section>
  );
}

// ---- Pill-group (radio) ----------------------------------------------------

export type PillOption<T extends string> = {
  value: T;
  label: string;
  // Tailwind classes for the *active* state. Inactive uses neutral panel.
  activeClass: string;
};

// Segmented pill picker used for severity. Behaves as a single-select radio
// group; keeps `value` / `onChange` semantics identical to a native select.
export function PillGroup<T extends string>({
  value,
  onChange,
  options,
  label,
}: {
  value: T;
  onChange: (v: T) => void;
  options: PillOption<T>[];
  label?: string;
}) {
  return (
    <div role="radiogroup" aria-label={label} className="inline-flex gap-1">
      {options.map((opt) => {
        const active = opt.value === value;
        return (
          <button
            key={opt.value}
            type="button"
            role="radio"
            aria-checked={active}
            onClick={() => onChange(opt.value)}
            className={`rounded-md px-2.5 py-1 text-xs font-medium ring-1 ring-inset transition-colors duration-150 ${
              active
                ? opt.activeClass
                : "bg-panel ring-border text-fg-subtle hover:text-fg hover:bg-panel-2"
            }`}
          >
            {opt.label}
          </button>
        );
      })}
    </div>
  );
}

// ---- Toggle ---------------------------------------------------------------

// Inline switch styled with theme tokens. Behaves like a checkbox for callers.
export function Toggle({
  checked,
  onChange,
  label,
  hint,
}: {
  checked: boolean;
  onChange: (v: boolean) => void;
  label: string;
  hint?: ReactNode;
}) {
  return (
    <label className="flex items-start gap-3 cursor-pointer select-none">
      <button
        type="button"
        role="switch"
        aria-checked={checked}
        onClick={() => onChange(!checked)}
        className={`relative mt-0.5 h-4 w-7 shrink-0 rounded-full ring-1 ring-inset transition-colors duration-150 ${
          checked
            ? "bg-accent/30 ring-accent/50"
            : "bg-panel-2 ring-border"
        }`}
      >
        <span
          className={`absolute top-0.5 h-3 w-3 rounded-full transition-all duration-150 ${
            checked ? "left-[14px] bg-accent" : "left-0.5 bg-fg-subtle"
          }`}
        />
      </button>
      <span className="flex flex-col">
        <span className="text-xs font-medium text-fg-muted">{label}</span>
        {hint && <span className="text-[11px] text-fg-subtle">{hint}</span>}
      </span>
    </label>
  );
}

// ---- Checkbox grid --------------------------------------------------------

export type CheckCard = {
  id: string;
  primary: ReactNode;
  secondary?: ReactNode;
  icon?: LucideIcon;
};

// Grid of checkable rows. Used for picking channels, hosts, and groups.
// Scrollable when the option list grows past `maxHeight`.
export function CheckboxGrid({
  options,
  selected,
  onToggle,
  empty,
  maxHeight = "max-h-64",
  columns = "sm:grid-cols-2",
}: {
  options: CheckCard[];
  selected: string[];
  onToggle: (id: string) => void;
  empty?: ReactNode;
  maxHeight?: string;
  columns?: string;
}) {
  if (options.length === 0) {
    return (
      <div className="rounded-md border border-dashed border-border bg-panel-2/30 px-3 py-4 text-center text-xs text-fg-subtle">
        {empty ?? "No options available."}
      </div>
    );
  }
  return (
    <div
      className={`overflow-y-auto rounded-md border border-border bg-panel-2/40 p-1 ${maxHeight}`}
    >
      <div className={`grid grid-cols-1 gap-1 ${columns}`}>
        {options.map((opt) => {
          const checked = selected.includes(opt.id);
          const Icon = opt.icon;
          return (
            <label
              key={opt.id}
              className={`flex cursor-pointer items-start gap-2 rounded-md border px-2 py-1.5 text-sm transition-colors duration-150 ${
                checked
                  ? "border-accent/60 bg-accent/10"
                  : "border-transparent hover:bg-panel-2"
              }`}
            >
              <input
                type="checkbox"
                checked={checked}
                onChange={() => onToggle(opt.id)}
                className="mt-0.5 h-3.5 w-3.5 accent-accent"
              />
              {Icon && (
                <Icon
                  className={`mt-0.5 h-3.5 w-3.5 shrink-0 ${
                    checked ? "text-accent" : "text-fg-subtle"
                  }`}
                />
              )}
              <span className="min-w-0 flex-1">
                <span className="block truncate text-xs font-medium text-fg">
                  {opt.primary}
                </span>
                {opt.secondary && (
                  <span className="mt-0.5 block text-[11px] text-fg-subtle">
                    {opt.secondary}
                  </span>
                )}
              </span>
            </label>
          );
        })}
      </div>
    </div>
  );
}

// ---- Tag chip --------------------------------------------------------------

export function TagChip({
  text,
  onRemove,
}: {
  text: string;
  onRemove?: () => void;
}) {
  return (
    <span className="inline-flex items-center gap-0.5 rounded-md bg-panel-2 pl-1.5 pr-0.5 py-0.5 font-mono text-[10px] text-accent ring-1 ring-inset ring-border">
      #{text}
      {onRemove && (
        <button
          type="button"
          onClick={onRemove}
          aria-label={`Remove tag ${text}`}
          className="rounded p-0.5 text-fg-subtle hover:bg-fail/20 hover:text-fail"
        >
          <svg viewBox="0 0 12 12" className="h-3 w-3" fill="none" stroke="currentColor" strokeWidth="1.5">
            <path d="M3 3l6 6M9 3l-6 6" strokeLinecap="round" />
          </svg>
        </button>
      )}
    </span>
  );
}

// ---- Type-card grid (for ChannelForm) -------------------------------------

export type TypeCardOption<T extends string> = {
  value: T;
  label: string;
  description: string;
  icon: LucideIcon;
};

export function TypeCardGrid<T extends string>({
  options,
  value,
  onChange,
  disabled = false,
}: {
  options: TypeCardOption<T>[];
  value: T;
  onChange: (v: T) => void;
  disabled?: boolean;
}) {
  return (
    <div className="grid grid-cols-1 gap-2 sm:grid-cols-2 lg:grid-cols-4">
      {options.map((opt) => {
        const Icon = opt.icon;
        const active = opt.value === value;
        return (
          <button
            type="button"
            key={opt.value}
            disabled={disabled}
            aria-pressed={active}
            onClick={() => !disabled && onChange(opt.value)}
            className={`group relative flex flex-col items-start gap-1.5 rounded-md border-2 bg-panel-2/40 p-3 text-left transition-colors duration-150 disabled:cursor-not-allowed disabled:opacity-60 ${
              active
                ? "border-accent bg-accent/5"
                : "border-border hover:border-border-strong hover:bg-panel-2"
            }`}
          >
            <Icon
              className={`h-4 w-4 ${
                active ? "text-accent" : "text-fg-subtle group-hover:text-fg-muted"
              }`}
            />
            <span className="text-sm font-medium text-fg">{opt.label}</span>
            <span className="text-[11px] leading-snug text-fg-subtle">
              {opt.description}
            </span>
          </button>
        );
      })}
    </div>
  );
}
