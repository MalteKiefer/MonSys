// RuleDraft holds the full wizard state. Components receive a `draft` plus
// a `patch(partial)` setter and write back through it — there is no per-step
// hidden state that would have to be merged on submit.
//
// As of multi-condition support the wizard manages an array of conditions:
//   draft.conditions = [{ conditionType, conditionParams, expertMode }, ...]
//
// `editingConditionIdx` drives the Step-1 sub-mode:
//   null     → collapsed list view (the bottom Next button is active)
//   "new"    → adding a fresh condition via the category picker
//   0/1/...  → editing the leg at that index in place
//
// expertMode lives per-condition so the user can flip a single leg into raw
// JSON without affecting siblings. The wizard-header Expert toggle then maps
// onto the currently-edited leg.

import type {
  NotificationConditionType,
  NotificationRule,
} from "../../../lib/types";
import { isConditionValid, stripConditionSuffix } from "./catalogue";
import { asRecord, type Params } from "./coerce";

export type Step = 1 | 2 | 3;

export type DraftCondition = {
  conditionType: NotificationConditionType | "";
  conditionParams: Params;
  expertMode: boolean;
};

export type RuleDraft = {
  step: Step;
  name: string;
  enabled: boolean;
  conditions: DraftCondition[];
  // Sub-mode for Step 1. null = collapsed list, "new" = category picker for
  // a fresh leg, number = editing that index in place.
  editingConditionIdx: number | "new" | null;
  // Buffer for the "new" / "editing" panel — kept separate from the array so
  // Cancel discards cleanly without mutating committed legs.
  buffer: DraftCondition;
  severity: "info" | "warning" | "critical";
  throttleSec: number;
  repeatIntervalSec: number;
  notifyOnResolve: boolean;
  targetMode: "all" | "tags" | "groups" | "hosts";
  targetTags: string[];
  targetGroupIds: string[];
  targetHostIds: string[];
  channelIds: string[];
};

export const EMPTY_BUFFER: DraftCondition = {
  conditionType: "",
  conditionParams: {},
  expertMode: false,
};

// Hydrate a draft from an existing rule (edit mode) or sensible defaults
// (new mode). targetMode is inferred from which target list is populated.
//
// For legacy single-rule edit (initial != null, no group_id), we seed
// draft.conditions with the one leg already collapsed (editingConditionIdx
// = null). The wizard shell is responsible for fetching sibling rules when
// initial.group_id is set and overriding conditions accordingly.
export function initialDraft(initial: NotificationRule | null): RuleDraft {
  const hostIds = initial?.target_host_ids ?? [];
  const tags = initial?.target_tags ?? [];
  const groupIds = initial?.target_group_ids ?? [];
  let targetMode: RuleDraft["targetMode"] = "all";
  if (hostIds.length > 0) targetMode = "hosts";
  else if (groupIds.length > 0) targetMode = "groups";
  else if (tags.length > 0) targetMode = "tags";

  const seedCondition: DraftCondition | null = initial?.condition_type
    ? {
        conditionType: initial.condition_type,
        conditionParams: asRecord(initial.condition_params),
        expertMode: false,
      }
    : null;

  // For a group leg, initial.name carries the per-row suffix the backend
  // appended (e.g. " — metric_threshold"). Strip it so the wizard surfaces
  // the user-meaningful base name; otherwise editing and re-saving would
  // compound suffixes ("X — type — type1 — type2").
  const baseName = initial?.group_id
    ? stripConditionSuffix(initial.name ?? "")
    : (initial?.name ?? "");

  return {
    step: 1,
    name: baseName,
    enabled: initial?.enabled ?? true,
    conditions: seedCondition ? [seedCondition] : [],
    editingConditionIdx: seedCondition ? null : "new",
    buffer: { ...EMPTY_BUFFER },
    severity: initial?.severity ?? "warning",
    throttleSec: initial?.throttle_sec ?? 300,
    repeatIntervalSec: initial?.repeat_interval_sec ?? 0,
    notifyOnResolve: initial?.notify_on_resolve ?? true,
    targetMode,
    targetTags: tags,
    targetGroupIds: groupIds,
    targetHostIds: hostIds,
    channelIds: initial?.channel_ids ?? [],
  };
}

// Replace the entire conditions array — used by the wizard shell when it
// hydrates a multi-leg group from the backend.
export function setConditions(d: RuleDraft, conds: DraftCondition[]): RuleDraft {
  return { ...d, conditions: conds, editingConditionIdx: conds.length === 0 ? "new" : null };
}

// Step 1 needs at least one committed condition AND every committed leg
// passing its own validation. The inline editor is allowed to be invalid
// while the user types — they just can't leave Step 1 in that state.
export function isStep1Valid(d: RuleDraft): boolean {
  if (d.conditions.length === 0) return false;
  if (d.editingConditionIdx !== null) return false; // must save/cancel first
  for (const c of d.conditions) {
    if (!c.conditionType) return false;
    if (!isConditionValid(c.conditionType, c.conditionParams)) return false;
  }
  return true;
}

// Step 2 always passes — empty targets means "all hosts".
export function isStep2Valid(_d: RuleDraft): boolean {
  return true;
}

// Step 3 needs a name and at least one channel.
export function isStep3Valid(d: RuleDraft): boolean {
  return d.name.trim().length > 0 && d.channelIds.length > 0;
}

// The buffer is "valid enough to save" once a type is picked AND the
// per-type validator is happy.
export function isBufferValid(b: DraftCondition): boolean {
  if (!b.conditionType) return false;
  return isConditionValid(b.conditionType, b.conditionParams);
}
