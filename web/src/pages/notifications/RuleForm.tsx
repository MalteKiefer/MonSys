// RuleForm is the top-level shell of the rule-creation wizard. It owns the
// RuleDraft state, the three step components, the live-preview pane, and
// the Save mutation.
//
// The wizard is three steps:
//   1. Detect — assemble a stack of conditions (each: category → type →
//      params, with its own Expert-JSON toggle).
//   2. Scope  — all hosts / tags / groups / specific hosts.
//   3. Notify — name, channels, severity, throttle, repeat, enabled.
//
// Save flow:
//   • Always POST /v1/notifications/rules/batch — the backend creates N
//     rule rows sharing one group_id (or just 1 row when there's one
//     condition). For edit-mode of a group, we DELETE every sibling row
//     first, then POST batch. For edit-mode of a legacy single rule with no
//     group_id, we fall through to PUT /v1/notifications/rules/{id} to
//     preserve the existing audit-log path.

import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import {
  ArrowLeft,
  ArrowRight,
  Code2,
  Save,
  Sliders,
  X,
} from "lucide-react";
import { FormEvent, useEffect, useState } from "react";

import {
  Button,
  ErrorBox,
  Panel,
  PanelBody,
  PanelHeader,
} from "../../components/ui";
import { useT } from "../../i18n/useT";
import { api, ApiError } from "../../lib/api";
import {
  Host,
  HostGroup,
  NotificationChannel,
  NotificationRule,
  NotificationRuleGroupInput,
  NotificationRuleGroupResponse,
  NotificationRuleInput,
} from "../../lib/types";

import {
  EMPTY_BUFFER,
  initialDraft,
  isStep1Valid,
  isStep2Valid,
  isStep3Valid,
  type DraftCondition,
  type RuleDraft,
  type Step,
} from "./RuleWizard/draft";
import { asRecord, type Params } from "./RuleWizard/coerce";
import { LivePreview } from "./RuleWizard/LivePreview";
import { StepDetect } from "./RuleWizard/StepDetect";
import { StepNotify } from "./RuleWizard/StepNotify";
import { StepScope } from "./RuleWizard/StepScope";
import { Stepper, type StepperItem } from "./RuleWizard/Stepper";

export function RuleForm({
  initial,
  allRules,
  channels,
  onCancel,
  onSaved,
}: {
  initial: NotificationRule | null;
  // allRules is needed in edit-mode so we can resolve sibling legs by
  // group_id without an extra fetch. The parent (RulesPage) already loads
  // the full list for its grouped display, so we just pass it through.
  allRules: NotificationRule[];
  channels: NotificationChannel[];
  onCancel: () => void;
  onSaved: () => void;
}) {
  const { t } = useT(["notifications", "common"]);
  // Translated step labels override STEP_LABELS for display purposes.
  const stepLabels: Record<Step, string> = {
    1: t("notifications:wizard.steps.detect"),
    2: t("notifications:wizard.steps.scope"),
    3: t("notifications:wizard.steps.notify"),
  };
  const tagsQuery = useQuery({
    queryKey: ["tags"],
    queryFn: () => api<{ tags: Array<{ tag: string; count: number }> }>("/v1/tags"),
  });
  const groupsQuery = useQuery({
    queryKey: ["groups"],
    queryFn: () => api<{ groups: HostGroup[] }>("/v1/groups"),
  });
  const hostsQuery = useQuery({
    queryKey: ["hosts"],
    queryFn: () => api<{ hosts: Host[] }>("/v1/hosts"),
  });

  const [draft, setDraft] = useState<RuleDraft>(() => initialDraft(initial));
  const [error, setError] = useState<string | null>(null);

  // Edit-mode hydration: when `initial` is a leg of a multi-condition group,
  // pull every sibling row and seed draft.conditions accordingly. We run
  // this once per `initial.id` so the user's subsequent edits aren't
  // clobbered. The `allRules` snapshot is sufficient — siblings share
  // group_id by construction.
  useEffect(() => {
    if (!initial?.group_id) return;
    const siblings = allRules.filter((r) => r.group_id === initial.group_id);
    if (siblings.length === 0) return;
    const conds: DraftCondition[] = siblings.map((r) => ({
      conditionType: r.condition_type,
      conditionParams: asRecord(r.condition_params),
      expertMode: false,
    }));
    setDraft((d) => ({
      ...d,
      conditions: conds,
      editingConditionIdx: null,
      buffer: { ...EMPTY_BUFFER },
    }));
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [initial?.id, initial?.group_id]);

  function patch(p: Partial<RuleDraft>) {
    setDraft((d) => ({ ...d, ...p }));
  }

  // The wizard-header Expert toggle proxies onto whichever leg is currently
  // being edited (draft.buffer). When no editor is open, the toggle is
  // disabled — see the rendered button below.
  const editorOpen = draft.editingConditionIdx !== null;
  const expertOn = editorOpen ? draft.buffer.expertMode : false;
  function toggleExpert() {
    if (!editorOpen) return;
    patch({ buffer: { ...draft.buffer, expertMode: !draft.buffer.expertMode } });
  }

  const step1Valid = isStep1Valid(draft);
  const step2Valid = isStep2Valid(draft);
  const step3Valid = isStep3Valid(draft);

  // Forward-shortcut in the stepper: clicking ahead works only when the
  // current step has validated.
  const canForward =
    (draft.step === 1 && step1Valid) || (draft.step === 2 && step2Valid);

  const qc = useQueryClient();

  const save = useMutation({
    mutationFn: async () => {
      if (draft.conditions.length === 0) {
        throw new Error(t("notifications:rules.form.error_add_condition"));
      }
      if (draft.channelIds.length === 0) {
        throw new Error(t("notifications:rules.form.error_pick_channel"));
      }
      if (
        draft.repeatIntervalSec !== 0 &&
        (draft.repeatIntervalSec < 60 || draft.repeatIntervalSec > 86400)
      ) {
        throw new Error(t("notifications:rules.form.error_repeat_range"));
      }

      // Round-trip every leg's params through JSON so we fail early on
      // unserialisable values. Mirrors what the backend will JSON-decode.
      const conditions = draft.conditions.map((c) => {
        let params: Params;
        try {
          params = JSON.parse(JSON.stringify(c.conditionParams ?? {})) as Params;
        } catch (e) {
          throw new Error(
            t("notifications:rules.form.error_params_not_serialisable", {
              type: c.conditionType,
              message: (e as Error).message,
            }),
          );
        }
        if (!c.conditionType) {
          throw new Error(t("notifications:rules.form.error_condition_no_type"));
        }
        return {
          condition_type: c.conditionType,
          condition_params: params,
        };
      });

      const targetHostIds =
        draft.targetMode === "hosts" ? draft.targetHostIds : [];
      const targetTags =
        draft.targetMode === "tags"
          ? draft.targetTags.map((t) => t.trim().toLowerCase()).filter(Boolean)
          : [];
      const targetGroupIds =
        draft.targetMode === "groups" ? draft.targetGroupIds : [];

      // Legacy single-rule edit fast-path: when editing a row with no
      // group_id AND we still have just one condition, PUT the existing
      // row so we don't churn the audit log with a delete-then-create pair.
      if (
        initial &&
        !initial.group_id &&
        conditions.length === 1
      ) {
        const c = conditions[0];
        const body: NotificationRuleInput = {
          name: draft.name,
          enabled: draft.enabled,
          condition_type: c.condition_type,
          condition_params: c.condition_params,
          channel_ids: draft.channelIds,
          severity: draft.severity,
          throttle_sec: draft.throttleSec,
          repeat_interval_sec: draft.repeatIntervalSec,
          notify_on_resolve: draft.notifyOnResolve,
          target_host_ids: targetHostIds,
          target_tags: targetTags,
          target_group_ids: targetGroupIds,
        };
        return api<NotificationRule>(
          `/v1/notifications/rules/${initial.id}`,
          { method: "PUT", body: JSON.stringify(body) },
        );
      }

      // Group edit (or upgrade from single → multi): hand the IDs to evict
      // to the batch endpoint. The server atomically DELETEs them BEFORE
      // running the new INSERTs in the same transaction, so a UNIQUE(name)
      // collision rolls everything back instead of leaving us in a half-
      // deleted state. No more N+1 round-trips, no more orphan rows.
      let replaceExistingIDs: string[] = [];
      if (initial) {
        const siblings = initial.group_id
          ? allRules.filter((r) => r.group_id === initial.group_id)
          : [initial];
        replaceExistingIDs = siblings.map((r) => r.id);
      }

      const body: NotificationRuleGroupInput = {
        name: draft.name,
        enabled: draft.enabled,
        severity: draft.severity,
        throttle_sec: draft.throttleSec,
        repeat_interval_sec: draft.repeatIntervalSec,
        notify_on_resolve: draft.notifyOnResolve,
        channel_ids: draft.channelIds,
        target_host_ids: targetHostIds,
        target_tags: targetTags,
        target_group_ids: targetGroupIds,
        conditions,
        replace_existing_ids: replaceExistingIDs.length > 0 ? replaceExistingIDs : undefined,
      };
      return api<NotificationRuleGroupResponse>(
        "/v1/notifications/rules/batch",
        { method: "POST", body: JSON.stringify(body) },
      );
    },
    onSuccess: () => {
      qc.invalidateQueries({ queryKey: ["rules"] });
      onSaved();
    },
    onError: (err) =>
      setError(err instanceof ApiError ? err.detail : (err as Error).message),
  });

  function onSubmit(e: FormEvent) {
    e.preventDefault();
    setError(null);
    save.mutate();
  }

  function goTo(s: Step) {
    setDraft((d) => ({ ...d, step: s }));
  }

  // Map the wizard's 1-based Step state onto the generic Stepper's 0-based
  // index API. `completed` lists future steps the user has already validated
  // (so they remain jumpable forward); past steps are always reachable.
  const stepperItems: StepperItem[] = [
    { key: "detect", label: stepLabels[1] },
    { key: "scope", label: stepLabels[2] },
    { key: "notify", label: stepLabels[3] },
  ];
  const currentIdx = draft.step - 1;
  const completedIdx: number[] = [];
  if (canForward) {
    for (let i = currentIdx + 1; i < stepperItems.length; i++) completedIdx.push(i);
  }
  function onJumpIdx(idx: number) {
    goTo((idx + 1) as Step);
  }

  return (
    <Panel>
      <PanelHeader>
        <div className="flex w-full items-center gap-3">
          <h3 className="text-sm font-semibold">
            {initial
              ? t("notifications:rules.form.title_edit", { name: initial.name })
              : t("notifications:rules.form.title_new")}
          </h3>
          <div className="hidden flex-1 px-4 md:block">
            <Stepper
              items={stepperItems}
              current={currentIdx}
              completed={completedIdx}
              onJump={onJumpIdx}
            />
          </div>
          <button
            type="button"
            role="switch"
            aria-checked={expertOn}
            disabled={!editorOpen}
            onClick={toggleExpert}
            className={`inline-flex items-center gap-1.5 rounded-md px-2 py-1 text-xs font-medium ring-1 ring-inset transition-colors duration-150 disabled:cursor-not-allowed disabled:opacity-50 ${
              expertOn
                ? "bg-accent/15 text-accent ring-accent/40"
                : "bg-panel ring-border text-fg-subtle hover:text-fg hover:bg-panel-2"
            }`}
            title={
              editorOpen
                ? t("notifications:rules.form.expert_tooltip_open")
                : t("notifications:rules.form.expert_tooltip_closed")
            }
          >
            {expertOn ? (
              <Code2 className="h-3.5 w-3.5" />
            ) : (
              <Sliders className="h-3.5 w-3.5" />
            )}
            {t("notifications:rules.form.expert_toggle")}
          </button>
          <button
            type="button"
            onClick={onCancel}
            aria-label={t("notifications:rules.form.close")}
            className="rounded-md p-1 text-fg-subtle hover:bg-panel-2 hover:text-fg"
          >
            <X className="h-4 w-4" />
          </button>
        </div>
      </PanelHeader>
      <PanelBody>
        {/* Mobile stepper — header version is desktop-only */}
        <div className="mb-3 md:hidden">
          <Stepper
            items={stepperItems}
            current={currentIdx}
            completed={completedIdx}
            onJump={onJumpIdx}
          />
        </div>

        <form onSubmit={onSubmit} className="space-y-4">
          <div className="grid grid-cols-1 gap-4 lg:grid-cols-[1fr_300px]">
            <div className="space-y-4">
              {draft.step === 1 && <StepDetect draft={draft} patch={patch} />}
              {draft.step === 2 && (
                <StepScope
                  draft={draft}
                  patch={patch}
                  tags={tagsQuery.data?.tags ?? []}
                  hosts={hostsQuery.data?.hosts ?? []}
                  groups={groupsQuery.data?.groups ?? []}
                />
              )}
              {draft.step === 3 && (
                <StepNotify draft={draft} patch={patch} channels={channels} />
              )}
            </div>
            <aside>
              <LivePreview
                draft={draft}
                channels={channels}
                hosts={hostsQuery.data?.hosts ?? []}
                groups={groupsQuery.data?.groups ?? []}
              />
            </aside>
          </div>

          {error && <ErrorBox>{error}</ErrorBox>}

          <div className="flex items-center justify-between gap-2 border-t border-border pt-3">
            {draft.step === 1 ? (
              <Button type="button" onClick={onCancel}>
                {t("notifications:rules.form.cancel")}
              </Button>
            ) : (
              <Button
                type="button"
                onClick={() => goTo((draft.step - 1) as Step)}
              >
                <ArrowLeft className="h-3.5 w-3.5" /> {t("notifications:rules.form.back")}
              </Button>
            )}
            {/* Hide the global Next/Save when Step 1's inline editor is
                open — the editor has its own Save/Cancel pair so the user
                can't skip committing a half-edited leg. */}
            {draft.step === 1 && editorOpen ? (
              <span className="text-[11px] text-fg-subtle">
                {t("notifications:rules.form.finish_condition_hint")}
              </span>
            ) : draft.step < 3 ? (
              <Button
                variant="primary"
                type="button"
                disabled={
                  (draft.step === 1 && !step1Valid) ||
                  (draft.step === 2 && !step2Valid)
                }
                onClick={() => goTo((draft.step + 1) as Step)}
              >
                {t("notifications:rules.form.next", { step: stepLabels[(draft.step + 1) as Step] })}{" "}
                <ArrowRight className="h-3.5 w-3.5" />
              </Button>
            ) : (
              <Button
                variant="primary"
                type="submit"
                disabled={save.isPending || !step1Valid || !step3Valid}
              >
                <Save className="h-3.5 w-3.5" />
                {save.isPending
                  ? t("notifications:rules.form.saving")
                  : initial
                    ? t("notifications:rules.form.save_rule")
                    : draft.conditions.length > 1
                      ? t("notifications:rules.form.create_rules_n", { count: draft.conditions.length })
                      : t("notifications:rules.form.create_rule")}
              </Button>
            )}
          </div>
        </form>
      </PanelBody>
    </Panel>
  );
}
