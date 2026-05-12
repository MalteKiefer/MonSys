// Step 1 — Detect: category picker → condition picker → params pane.
// The Expert-JSON toggle in the wizard header replaces this whole step
// with a raw-JSON editor (still typed against the same conditionParams).

import { useEffect, useState } from "react";

import type { NotificationConditionType } from "../../../lib/types";

import {
  CATEGORY_CARDS,
  CATEGORY_MAP,
  CONDITION_TYPES,
  type CategoryKey,
  categoryOf,
} from "./catalogue";
import type { RuleDraft } from "./draft";
import { ConditionParamsPane, ExpertJsonPane } from "./Panes";
import { CategoryCard } from "./Stepper";

export function StepDetect({
  draft,
  patch,
}: {
  draft: RuleDraft;
  patch: (p: Partial<RuleDraft>) => void;
}) {
  // The selected category is derived from the current conditionType (in edit
  // mode this lights up the right card). Users can also pre-select a
  // category before picking a type; we track that selection locally.
  const derivedCategory = draft.conditionType ? categoryOf(draft.conditionType) : null;
  const [selectedCategory, setSelectedCategory] = useState<CategoryKey | null>(derivedCategory);

  // When the externally-chosen condition_type changes (e.g. JSON round-trip
  // or initial-mount hydration) refresh the local category selection so the
  // UI doesn't lie.
  useEffect(() => {
    if (derivedCategory && derivedCategory !== selectedCategory) {
      setSelectedCategory(derivedCategory);
    }
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [derivedCategory]);

  const conditionMeta = draft.conditionType
    ? CONDITION_TYPES.find((c) => c.value === draft.conditionType)
    : null;

  if (draft.expertMode) {
    return (
      <div className="space-y-4">
        <div className="rounded-md border border-border bg-panel-2/40 p-3">
          <p className="mb-2 text-[11px] font-semibold uppercase tracking-[0.08em] text-fg-subtle">
            Condition type
          </p>
          <select
            value={draft.conditionType}
            onChange={(e) => {
              const next = e.target.value as NotificationConditionType;
              patch({ conditionType: next, conditionParams: {} });
            }}
            className="w-full rounded-md border border-border bg-panel px-3 py-2 text-sm focus:border-accent focus:outline-none"
          >
            <option value="">— pick one —</option>
            {CONDITION_TYPES.map((c) => (
              <option key={c.value} value={c.value}>
                {c.label}
              </option>
            ))}
          </select>
          {conditionMeta && (
            <p className="mt-2 text-[11px] text-fg-subtle">{conditionMeta.description}</p>
          )}
        </div>
        <ExpertJsonPane
          params={draft.conditionParams}
          setParams={(p) => patch({ conditionParams: p })}
        />
      </div>
    );
  }

  // Phase 1a — category picker (always visible, can be re-clicked to pivot).
  // Phase 1b — condition list within selected category.
  // Phase 1c — params pane for the selected condition.

  return (
    <div className="space-y-5">
      <section>
        <p className="mb-2 text-[11px] font-semibold uppercase tracking-[0.08em] text-fg-subtle">
          1. What kind of signal?
        </p>
        <div className="grid grid-cols-2 gap-2 sm:grid-cols-3">
          {CATEGORY_CARDS.map((c) => (
            <CategoryCard
              key={c.key}
              label={c.label}
              blurb={c.blurb}
              Icon={c.icon}
              selected={selectedCategory === c.key}
              onClick={() => {
                setSelectedCategory(c.key);
                // Picking a new category drops the previously-selected type
                // so the conditions list isn't pre-confirmed under the new
                // category banner.
                const stillInCategory =
                  draft.conditionType && CATEGORY_MAP[c.key].includes(draft.conditionType);
                if (!stillInCategory) {
                  patch({ conditionType: "", conditionParams: {} });
                }
              }}
            />
          ))}
        </div>
      </section>

      {selectedCategory && (
        <section>
          <div className="mb-2 flex items-baseline justify-between">
            <p className="text-[11px] font-semibold uppercase tracking-[0.08em] text-fg-subtle">
              2. Pick a condition
            </p>
            <button
              type="button"
              onClick={() => {
                setSelectedCategory(null);
                patch({ conditionType: "", conditionParams: {} });
              }}
              className="text-[11px] text-fg-subtle hover:text-fg"
            >
              ← Back to categories
            </button>
          </div>
          <div className="space-y-1.5">
            {CATEGORY_MAP[selectedCategory].map((ct) => {
              const meta = CONDITION_TYPES.find((c) => c.value === ct)!;
              const active = draft.conditionType === ct;
              return (
                <button
                  key={ct}
                  type="button"
                  onClick={() => {
                    if (draft.conditionType !== ct) {
                      patch({ conditionType: ct, conditionParams: {} });
                    }
                  }}
                  aria-pressed={active}
                  className={`flex w-full items-start gap-3 rounded-md border px-3 py-2 text-left transition-colors duration-150 focus:outline-none focus:ring-2 focus:ring-accent/40 ${
                    active
                      ? "border-accent bg-accent/5"
                      : "border-border bg-panel-2/40 hover:bg-panel-2 hover:border-border-strong"
                  }`}
                >
                  <span
                    aria-hidden
                    className={`mt-0.5 h-3 w-3 shrink-0 rounded-full ring-1 ring-inset ${
                      active ? "bg-accent ring-accent/50" : "bg-panel ring-border"
                    }`}
                  />
                  <span className="min-w-0 flex-1">
                    <span className="block text-sm font-medium text-fg">{meta.label}</span>
                    <span className="mt-0.5 block text-[11px] text-fg-subtle">
                      {meta.description}
                    </span>
                  </span>
                </button>
              );
            })}
          </div>
        </section>
      )}

      {draft.conditionType && (
        <section>
          <p className="mb-2 text-[11px] font-semibold uppercase tracking-[0.08em] text-fg-subtle">
            3. Configure {conditionMeta?.label.toLowerCase()}
          </p>
          <ConditionParamsPane
            conditionType={draft.conditionType}
            params={draft.conditionParams}
            setParams={(p) => patch({ conditionParams: p })}
          />
        </section>
      )}
    </div>
  );
}
