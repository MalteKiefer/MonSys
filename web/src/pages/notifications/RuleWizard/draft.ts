// RuleDraft holds the full wizard state. Components receive a `draft` plus
// a `patch(partial)` setter and write back through it — there is no per-step
// hidden state that would have to be merged on submit.

import type {
  NotificationConditionType,
  NotificationRule,
} from "../../../lib/types";
import { asStringArray, type Params } from "./coerce";

export type Step = 1 | 2 | 3;

export type RuleDraft = {
  step: Step;
  expertMode: boolean;
  name: string;
  enabled: boolean;
  conditionType: NotificationConditionType | "";
  conditionParams: Params;
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

// Hydrate a draft from an existing rule (edit mode) or sensible defaults
// (new mode). targetMode is inferred from which target list is populated.
export function initialDraft(initial: NotificationRule | null): RuleDraft {
  const hostIds = initial?.target_host_ids ?? [];
  const tags = initial?.target_tags ?? [];
  const groupIds = initial?.target_group_ids ?? [];
  let targetMode: RuleDraft["targetMode"] = "all";
  if (hostIds.length > 0) targetMode = "hosts";
  else if (groupIds.length > 0) targetMode = "groups";
  else if (tags.length > 0) targetMode = "tags";
  return {
    step: 1,
    expertMode: false,
    name: initial?.name ?? "",
    enabled: initial?.enabled ?? true,
    conditionType: initial?.condition_type ?? "",
    conditionParams: (initial?.condition_params as Params) ?? {},
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

// Step 1 needs a condition type AND, for the few parameterised types that
// have a required field with no sensible default, that field present. Most
// other defaults are auto-supplied by the panes themselves.
export function isStep1Valid(d: RuleDraft): boolean {
  if (!d.conditionType) return false;
  if (d.conditionType === "metric_threshold") {
    const v = d.conditionParams.value;
    if (v === undefined || v === null || v === "") return false;
  }
  if (d.conditionType === "audit_action") {
    const actions = asStringArray(d.conditionParams.actions);
    if (actions.length === 0) return false;
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
