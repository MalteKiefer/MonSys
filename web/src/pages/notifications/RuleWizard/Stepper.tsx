// Horizontal stepper "Detect → Scope → Notify". Done steps render a check
// icon, the current step is accent-filled, future steps are outlined. Past
// steps are always clickable (jump back); future steps are clickable only
// when the current step has validated (forward shortcut).

import { Activity, Check } from "lucide-react";
import type { ReactNode } from "react";

import type { Step } from "./draft";

export const STEP_LABELS: Record<Step, string> = {
  1: "Detect",
  2: "Scope",
  3: "Notify",
};

export function Stepper({
  step,
  canForward,
  onJump,
}: {
  step: Step;
  canForward: boolean;
  onJump: (s: Step) => void;
}) {
  const steps: Step[] = [1, 2, 3];
  return (
    <ol className="flex w-full items-center gap-2" aria-label="Wizard progress">
      {steps.map((s, idx) => {
        const isDone = s < step;
        const isCurrent = s === step;
        const canClick = s < step || (s > step && canForward);
        return (
          <li key={s} className="flex flex-1 items-center gap-2">
            <button
              type="button"
              disabled={!canClick && !isCurrent}
              onClick={() => canClick && onJump(s)}
              className={`group flex items-center gap-2 rounded-md px-1 py-0.5 text-left transition-colors duration-150 focus:outline-none focus:ring-2 focus:ring-accent/40 ${
                canClick ? "cursor-pointer" : "cursor-default"
              }`}
              aria-current={isCurrent ? "step" : undefined}
            >
              <span
                aria-hidden
                className={`flex h-6 w-6 shrink-0 items-center justify-center rounded-full text-[11px] font-semibold ring-1 ring-inset transition-colors duration-150 ${
                  isDone
                    ? "bg-ok/20 text-ok ring-ok/40"
                    : isCurrent
                      ? "bg-accent/20 text-accent ring-accent/50"
                      : "bg-panel-2 text-fg-subtle ring-border"
                }`}
              >
                {isDone ? <Check className="h-3.5 w-3.5" /> : s}
              </span>
              <span
                className={`text-xs font-medium ${
                  isCurrent ? "text-fg" : isDone ? "text-fg-muted" : "text-fg-subtle"
                }`}
              >
                {STEP_LABELS[s]}
              </span>
            </button>
            {idx < steps.length - 1 && (
              <span
                aria-hidden
                className={`h-px flex-1 ${s < step ? "bg-ok/50" : "bg-border"}`}
              />
            )}
          </li>
        );
      })}
    </ol>
  );
}

// CategoryCard is a big tile in the Step-1 category picker. The icon prop is
// any lucide icon component (same shape as `Activity`).
export function CategoryCard({
  label,
  blurb,
  Icon,
  selected,
  onClick,
}: {
  label: string;
  blurb: string;
  Icon: typeof Activity;
  selected: boolean;
  onClick: () => void;
}): ReactNode {
  return (
    <button
      type="button"
      onClick={onClick}
      aria-pressed={selected}
      className={`group flex flex-col items-start gap-1.5 rounded-md border-2 bg-panel-2/40 p-3 text-left transition-colors duration-150 focus:outline-none focus:ring-2 focus:ring-accent/40 ${
        selected ? "border-accent bg-accent/5" : "border-border hover:border-border-strong hover:bg-panel-2"
      }`}
    >
      <Icon className={`h-5 w-5 ${selected ? "text-accent" : "text-fg-subtle group-hover:text-fg-muted"}`} />
      <span className="text-sm font-medium text-fg">{label}</span>
      <span className="text-[11px] leading-snug text-fg-subtle">{blurb}</span>
    </button>
  );
}
