// Rule-wizard-specific helpers. The generic `Stepper` lives in the shared UI
// primitives (`components/ui`) and is re-exported here for backward
// compatibility with the import paths inside this module.
//
// STEP_LABELS and CategoryCard stay local because they are tied to the rule
// wizard's vocabulary (Detect/Scope/Notify) and the Step-1 category picker
// layout — neither belongs in the shared library.

import type { Activity } from "lucide-react";
import type { ReactNode } from "react";

import type { Step } from "./draft";

export { Stepper, type StepperItem } from "../../../components/ui";

export const STEP_LABELS: Record<Step, string> = {
  1: "Detect",
  2: "Scope",
  3: "Notify",
};

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
