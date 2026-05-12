// Step 1 — Detect: the user assembles a stack of conditions. The list shows
// committed legs as collapsed cards (with edit / remove). Clicking
// "Add another condition" (or having an empty list) opens an inline editor
// that runs the original 3-phase flow:
//
//   1. Pick a category.
//   2. Pick a condition type within the category.
//   3. Configure per-type params (or flip to Expert JSON for that one leg).
//
// The editor has its own Save/Cancel footer; the wizard's bottom "Next →"
// button is hidden while it's open (the parent inspects
// draft.editingConditionIdx).
//
// All committed legs OR the picked-but-uncommitted draft.buffer can be in
// Expert mode independently. The wizard-header Expert toggle proxies onto
// whichever leg is currently being edited.

import { Code2, Pencil, Plus, Sliders, X } from "lucide-react";
import { useEffect, useState } from "react";

import { Button } from "../../../components/ui";
import { useT } from "../../../i18n/useT";
import type { NotificationConditionType } from "../../../lib/types";

import {
  CATEGORY_CARDS,
  CATEGORY_MAP,
  CONDITION_TYPES,
  conditionIcon,
  conditionLabel,
  conditionSummary,
  type CategoryKey,
  categoryOf,
} from "./catalogue";
import {
  EMPTY_BUFFER,
  isBufferValid,
  type DraftCondition,
  type RuleDraft,
} from "./draft";
import { ConditionParamsPane, ExpertJsonPane } from "./Panes";
import { CategoryCard } from "./Stepper";

export function StepDetect({
  draft,
  patch,
}: {
  draft: RuleDraft;
  patch: (p: Partial<RuleDraft>) => void;
}) {
  const { t } = useT(["notifications", "common"]);
  const editing = draft.editingConditionIdx;
  const isEditing = editing !== null;

  // When the user is editing an existing leg, the buffer mirrors that leg
  // until Save commits the changes back into draft.conditions. We sync via
  // an effect rather than during render so React doesn't complain.
  useEffect(() => {
    if (typeof editing === "number") {
      const target = draft.conditions[editing];
      if (target) patch({ buffer: { ...target } });
    } else if (editing === "new") {
      // Reset only when entering "new" mode from a clean slate; if the user
      // is already mid-edit don't clobber what they typed.
      if (
        draft.buffer.conditionType !== "" ||
        Object.keys(draft.buffer.conditionParams).length > 0
      ) {
        // keep existing buffer
      } else {
        patch({ buffer: { ...EMPTY_BUFFER } });
      }
    }
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [editing]);

  function openEditor(idx: number) {
    patch({ editingConditionIdx: idx, buffer: { ...draft.conditions[idx] } });
  }

  function openNew() {
    patch({ editingConditionIdx: "new", buffer: { ...EMPTY_BUFFER } });
  }

  function removeAt(idx: number) {
    const next = draft.conditions.filter((_, i) => i !== idx);
    patch({
      conditions: next,
      editingConditionIdx: next.length === 0 ? "new" : null,
      buffer: next.length === 0 ? { ...EMPTY_BUFFER } : draft.buffer,
    });
  }

  function cancelEditor() {
    // If the user was adding a new leg and there are already committed legs
    // available, dropping back to the list view is the natural exit. If
    // they're editing an existing leg, we also drop back. If the list is
    // empty AND they cancel, we leave them in "new" mode (otherwise Step 1
    // is unreachable forward).
    if (draft.conditions.length === 0) {
      patch({ buffer: { ...EMPTY_BUFFER } });
      return;
    }
    patch({ editingConditionIdx: null, buffer: { ...EMPTY_BUFFER } });
  }

  function saveEditor() {
    if (!isBufferValid(draft.buffer)) return;
    if (editing === "new") {
      const next = [...draft.conditions, { ...draft.buffer }];
      patch({
        conditions: next,
        editingConditionIdx: null,
        buffer: { ...EMPTY_BUFFER },
      });
    } else if (typeof editing === "number") {
      const next = draft.conditions.map((c, i) =>
        i === editing ? { ...draft.buffer } : c,
      );
      patch({
        conditions: next,
        editingConditionIdx: null,
        buffer: { ...EMPTY_BUFFER },
      });
    }
  }

  return (
    <div className="space-y-5">
      {/* COLLAPSED LIST — visible whenever we have committed legs. */}
      {draft.conditions.length > 0 && (
        <section>
          <div className="mb-2 flex items-baseline justify-between">
            <p className="text-[11px] font-semibold uppercase tracking-[0.08em] text-fg-subtle">
              {t("notifications:wizard.detect.conditions_label")}{" "}
              <span className="text-fg-subtle/70 normal-case">
                {t("notifications:wizard.detect.conditions_hint")}
              </span>
            </p>
            <span className="text-[10px] text-fg-subtle">
              {t("notifications:wizard.detect.configured_count", { count: draft.conditions.length })}
            </span>
          </div>
          <ul className="space-y-1.5">
            {draft.conditions.map((c, idx) => {
              const editingThis = editing === idx;
              const Icon = c.conditionType
                ? conditionIcon(c.conditionType)
                : null;
              return (
                <li
                  key={idx}
                  className={`rounded-md border bg-panel-2/40 px-3 py-2 transition-colors duration-150 ${
                    editingThis ? "border-accent ring-1 ring-accent/20" : "border-border"
                  }`}
                >
                  <div className="flex items-start gap-2">
                    {Icon && (
                      <Icon className="mt-0.5 h-4 w-4 shrink-0 text-accent" />
                    )}
                    <div className="min-w-0 flex-1">
                      <p className="text-sm font-medium text-fg">
                        {c.conditionType
                          ? conditionLabel(c.conditionType)
                          : t("notifications:wizard.detect.no_type")}
                      </p>
                      <p className="mt-0.5 text-[11px] text-fg-subtle">
                        {c.conditionType
                          ? conditionSummary(c.conditionType, c.conditionParams)
                          : t("notifications:wizard.detect.no_summary")}
                      </p>
                    </div>
                    <div className="flex shrink-0 items-center gap-1">
                      <button
                        type="button"
                        onClick={() => openEditor(idx)}
                        aria-label={t("notifications:wizard.detect.edit_condition_aria", { index: idx + 1 })}
                        disabled={isEditing && !editingThis}
                        className="rounded-md p-1 text-fg-subtle hover:bg-panel-2 hover:text-fg disabled:opacity-40"
                      >
                        <Pencil className="h-3.5 w-3.5" />
                      </button>
                      <button
                        type="button"
                        onClick={() => removeAt(idx)}
                        aria-label={t("notifications:wizard.detect.remove_condition_aria", { index: idx + 1 })}
                        disabled={isEditing && !editingThis}
                        className="rounded-md p-1 text-fg-subtle hover:bg-fail/15 hover:text-fail disabled:opacity-40"
                      >
                        <X className="h-3.5 w-3.5" />
                      </button>
                    </div>
                  </div>
                </li>
              );
            })}
          </ul>
          {!isEditing && (
            <button
              type="button"
              onClick={openNew}
              className="mt-2 inline-flex items-center gap-1.5 rounded-md border border-dashed border-border bg-panel-2/30 px-3 py-1.5 text-xs font-medium text-fg-muted hover:border-accent/50 hover:bg-accent/5 hover:text-fg"
            >
              <Plus className="h-3.5 w-3.5" /> {t("notifications:wizard.detect.add_another")}
            </button>
          )}
        </section>
      )}

      {/* INLINE EDITOR — open whenever editingConditionIdx !== null. */}
      {isEditing && (
        <ConditionEditor
          buffer={draft.buffer}
          setBuffer={(b) => patch({ buffer: b })}
          isNew={editing === "new"}
          canCancel={draft.conditions.length > 0 || editing !== "new"}
          onSave={saveEditor}
          onCancel={cancelEditor}
        />
      )}
    </div>
  );
}

function ConditionEditor({
  buffer,
  setBuffer,
  isNew,
  canCancel,
  onSave,
  onCancel,
}: {
  buffer: DraftCondition;
  setBuffer: (b: DraftCondition) => void;
  isNew: boolean;
  canCancel: boolean;
  onSave: () => void;
  onCancel: () => void;
}) {
  const { t } = useT(["notifications", "common"]);
  const derivedCategory = buffer.conditionType
    ? categoryOf(buffer.conditionType)
    : null;
  const [selectedCategory, setSelectedCategory] = useState<CategoryKey | null>(
    derivedCategory,
  );

  // Re-derive when the external buffer changes (e.g. user just opened an
  // existing leg for edit) so the right category is highlighted.
  useEffect(() => {
    if (derivedCategory && derivedCategory !== selectedCategory) {
      setSelectedCategory(derivedCategory);
    }
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [derivedCategory]);

  const conditionMeta = buffer.conditionType
    ? CONDITION_TYPES.find((c) => c.value === buffer.conditionType)
    : null;

  const canSave = isBufferValid(buffer);

  return (
    <section className="rounded-md border-2 border-dashed border-accent/30 bg-accent/5 p-3">
      <div className="mb-3 flex items-center justify-between gap-2">
        <p className="text-[11px] font-semibold uppercase tracking-[0.08em] text-fg-subtle">
          {isNew
            ? t("notifications:wizard.detect.editor_title_new")
            : t("notifications:wizard.detect.editor_title_edit")}
        </p>
        <button
          type="button"
          role="switch"
          aria-checked={buffer.expertMode}
          onClick={() => setBuffer({ ...buffer, expertMode: !buffer.expertMode })}
          className={`inline-flex items-center gap-1.5 rounded-md px-2 py-1 text-[11px] font-medium ring-1 ring-inset transition-colors duration-150 ${
            buffer.expertMode
              ? "bg-accent/15 text-accent ring-accent/40"
              : "bg-panel ring-border text-fg-subtle hover:text-fg hover:bg-panel-2"
          }`}
          title={t("notifications:wizard.detect.expert_tooltip")}
        >
          {buffer.expertMode ? (
            <Code2 className="h-3.5 w-3.5" />
          ) : (
            <Sliders className="h-3.5 w-3.5" />
          )}
          {t("notifications:wizard.detect.expert_toggle")}
        </button>
      </div>

      {buffer.expertMode ? (
        <div className="space-y-3">
          <div className="rounded-md border border-border bg-panel-2/40 p-3">
            <p className="mb-2 text-[11px] font-semibold uppercase tracking-[0.08em] text-fg-subtle">
              {t("notifications:wizard.detect.section_condition_type")}
            </p>
            <select
              value={buffer.conditionType}
              onChange={(e) => {
                const next = e.target.value as NotificationConditionType | "";
                setBuffer({ ...buffer, conditionType: next, conditionParams: {} });
              }}
              className="w-full rounded-md border border-border bg-panel px-3 py-2 text-sm focus:border-accent focus:outline-none"
            >
              <option value="">{t("notifications:wizard.detect.select_placeholder")}</option>
              {CONDITION_TYPES.map((c) => (
                <option key={c.value} value={c.value}>
                  {c.label}
                </option>
              ))}
            </select>
            {conditionMeta && (
              <p className="mt-2 text-[11px] text-fg-subtle">
                {conditionMeta.description}
              </p>
            )}
          </div>
          <ExpertJsonPane
            params={buffer.conditionParams}
            setParams={(p) => setBuffer({ ...buffer, conditionParams: p })}
          />
        </div>
      ) : (
        <div className="space-y-4">
          <section>
            <p className="mb-2 text-[11px] font-semibold uppercase tracking-[0.08em] text-fg-subtle">
              {t("notifications:wizard.detect.step1_heading")}
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
                    const stillInCategory =
                      buffer.conditionType &&
                      CATEGORY_MAP[c.key].includes(buffer.conditionType);
                    if (!stillInCategory) {
                      setBuffer({
                        ...buffer,
                        conditionType: "",
                        conditionParams: {},
                      });
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
                  {t("notifications:wizard.detect.step2_heading")}
                </p>
                <button
                  type="button"
                  onClick={() => {
                    setSelectedCategory(null);
                    setBuffer({ ...buffer, conditionType: "", conditionParams: {} });
                  }}
                  className="text-[11px] text-fg-subtle hover:text-fg"
                >
                  {t("notifications:wizard.detect.back_to_categories")}
                </button>
              </div>
              <div className="space-y-1.5">
                {CATEGORY_MAP[selectedCategory].map((ct) => {
                  const meta = CONDITION_TYPES.find((c) => c.value === ct)!;
                  const active = buffer.conditionType === ct;
                  return (
                    <button
                      key={ct}
                      type="button"
                      onClick={() => {
                        if (buffer.conditionType !== ct) {
                          setBuffer({
                            ...buffer,
                            conditionType: ct,
                            conditionParams: {},
                          });
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
                        <span className="block text-sm font-medium text-fg">
                          {meta.label}
                        </span>
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

          {buffer.conditionType && (
            <section>
              <p className="mb-2 text-[11px] font-semibold uppercase tracking-[0.08em] text-fg-subtle">
                {t("notifications:wizard.detect.step3_heading", { label: conditionMeta?.label.toLowerCase() ?? "" })}
              </p>
              <ConditionParamsPane
                conditionType={buffer.conditionType}
                params={buffer.conditionParams}
                setParams={(p) => setBuffer({ ...buffer, conditionParams: p })}
              />
            </section>
          )}
        </div>
      )}

      <div className="mt-4 flex items-center justify-end gap-2 border-t border-accent/20 pt-3">
        {canCancel && (
          <Button type="button" onClick={onCancel}>
            {t("notifications:wizard.detect.cancel")}
          </Button>
        )}
        <Button
          variant="primary"
          type="button"
          disabled={!canSave}
          onClick={onSave}
        >
          {isNew
            ? t("notifications:wizard.detect.save_condition")
            : t("notifications:wizard.detect.update_condition")}
        </Button>
      </div>
    </section>
  );
}
