// RuleForm is the top-level shell of the rule-creation wizard. It owns the
// RuleDraft state, the three step components, the live-preview pane, and
// the Save mutation. All pane logic lives in ./RuleWizard/*.
//
// The wizard is three steps:
//   1. Detect — pick a category, then a condition type, then per-type params.
//   2. Scope — all hosts / tags / groups / specific hosts.
//   3. Notify — name, channels, severity, throttle, repeat, enabled.
//
// An Expert (JSON) toggle in the header swaps Step 1's typed pane for a
// raw JSON editor without losing user input — both modes write the same
// conditionParams object.

import { useMutation, useQuery } from "@tanstack/react-query";
import {
  ArrowLeft,
  ArrowRight,
  Code2,
  Save,
  Sliders,
  X,
} from "lucide-react";
import { FormEvent, useState } from "react";

import {
  Button,
  ErrorBox,
  Panel,
  PanelBody,
  PanelHeader,
} from "../../components/ui";
import { api, ApiError } from "../../lib/api";
import {
  Host,
  HostGroup,
  NotificationChannel,
  NotificationRule,
  NotificationRuleInput,
} from "../../lib/types";

import {
  initialDraft,
  isStep1Valid,
  isStep2Valid,
  isStep3Valid,
  type RuleDraft,
  type Step,
} from "./RuleWizard/draft";
import type { Params } from "./RuleWizard/coerce";
import { LivePreview } from "./RuleWizard/LivePreview";
import { StepDetect } from "./RuleWizard/StepDetect";
import { StepNotify } from "./RuleWizard/StepNotify";
import { StepScope } from "./RuleWizard/StepScope";
import { STEP_LABELS, Stepper } from "./RuleWizard/Stepper";

export function RuleForm({
  initial,
  channels,
  onCancel,
  onSaved,
}: {
  initial: NotificationRule | null;
  channels: NotificationChannel[];
  onCancel: () => void;
  onSaved: () => void;
}) {
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

  function patch(p: Partial<RuleDraft>) {
    setDraft((d) => ({ ...d, ...p }));
  }

  const step1Valid = isStep1Valid(draft);
  const step2Valid = isStep2Valid(draft);
  const step3Valid = isStep3Valid(draft);

  // Forward-shortcut: the stepper allows clicking ahead when the current
  // step validates. Computed once per render so each step button knows
  // whether to accept clicks.
  const canForward =
    (draft.step === 1 && step1Valid) || (draft.step === 2 && step2Valid);

  const save = useMutation({
    mutationFn: () => {
      if (!draft.conditionType) throw new Error("Pick a condition type");
      if (draft.channelIds.length === 0) {
        throw new Error("Pick at least one channel");
      }
      if (
        draft.repeatIntervalSec !== 0 &&
        (draft.repeatIntervalSec < 60 || draft.repeatIntervalSec > 86400)
      ) {
        throw new Error("Repeat interval must be 0 or between 60 and 86400 seconds");
      }
      // Final sanity: roundtrip the params object through JSON so we fail
      // early on circular refs or unserialisable values (also matches what
      // the backend expects).
      let params: Params;
      try {
        params = JSON.parse(JSON.stringify(draft.conditionParams ?? {})) as Params;
      } catch (e) {
        throw new Error(`condition_params is not serialisable JSON: ${(e as Error).message}`);
      }
      // Only ship the target list relevant for the chosen mode; the others
      // are empty so the backend interprets the rule cleanly.
      const body: NotificationRuleInput = {
        name: draft.name,
        enabled: draft.enabled,
        condition_type: draft.conditionType,
        condition_params: params,
        channel_ids: draft.channelIds,
        severity: draft.severity,
        throttle_sec: draft.throttleSec,
        repeat_interval_sec: draft.repeatIntervalSec,
        notify_on_resolve: draft.notifyOnResolve,
        target_host_ids: draft.targetMode === "hosts" ? draft.targetHostIds : [],
        target_tags:
          draft.targetMode === "tags"
            ? draft.targetTags.map((t) => t.trim().toLowerCase()).filter(Boolean)
            : [],
        target_group_ids: draft.targetMode === "groups" ? draft.targetGroupIds : [],
      };
      if (initial) {
        return api(`/v1/notifications/rules/${initial.id}`, {
          method: "PUT",
          body: JSON.stringify(body),
        });
      }
      return api("/v1/notifications/rules", {
        method: "POST",
        body: JSON.stringify(body),
      });
    },
    onSuccess: onSaved,
    onError: (err) => setError(err instanceof ApiError ? err.detail : (err as Error).message),
  });

  function onSubmit(e: FormEvent) {
    e.preventDefault();
    setError(null);
    save.mutate();
  }

  function goTo(s: Step) {
    setDraft((d) => ({ ...d, step: s }));
  }

  return (
    <Panel>
      <PanelHeader>
        <div className="flex w-full items-center gap-3">
          <h3 className="text-sm font-semibold">
            {initial ? `Edit ${initial.name}` : "New rule"}
          </h3>
          <div className="hidden flex-1 px-4 md:block">
            <Stepper step={draft.step} canForward={canForward} onJump={goTo} />
          </div>
          <button
            type="button"
            role="switch"
            aria-checked={draft.expertMode}
            onClick={() => patch({ expertMode: !draft.expertMode })}
            className={`inline-flex items-center gap-1.5 rounded-md px-2 py-1 text-xs font-medium ring-1 ring-inset transition-colors duration-150 ${
              draft.expertMode
                ? "bg-accent/15 text-accent ring-accent/40"
                : "bg-panel ring-border text-fg-subtle hover:text-fg hover:bg-panel-2"
            }`}
            title="Toggle expert / raw JSON mode"
          >
            {draft.expertMode ? <Code2 className="h-3.5 w-3.5" /> : <Sliders className="h-3.5 w-3.5" />}
            Expert JSON
          </button>
          <button
            type="button"
            onClick={onCancel}
            aria-label="Close"
            className="rounded-md p-1 text-fg-subtle hover:bg-panel-2 hover:text-fg"
          >
            <X className="h-4 w-4" />
          </button>
        </div>
      </PanelHeader>
      <PanelBody>
        {/* Mobile stepper — header version is desktop-only */}
        <div className="mb-3 md:hidden">
          <Stepper step={draft.step} canForward={canForward} onJump={goTo} />
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
                Cancel
              </Button>
            ) : (
              <Button
                type="button"
                onClick={() => goTo((draft.step - 1) as Step)}
              >
                <ArrowLeft className="h-3.5 w-3.5" /> Back
              </Button>
            )}
            {draft.step < 3 ? (
              <Button
                variant="primary"
                type="button"
                disabled={
                  (draft.step === 1 && !step1Valid) ||
                  (draft.step === 2 && !step2Valid)
                }
                onClick={() => goTo((draft.step + 1) as Step)}
              >
                Next: {STEP_LABELS[(draft.step + 1) as Step]} <ArrowRight className="h-3.5 w-3.5" />
              </Button>
            ) : (
              <Button
                variant="primary"
                type="submit"
                disabled={save.isPending || !step1Valid || !step3Valid}
              >
                <Save className="h-3.5 w-3.5" />
                {save.isPending ? "Saving…" : initial ? "Save rule" : "Create rule"}
              </Button>
            )}
          </div>
        </form>
      </PanelBody>
    </Panel>
  );
}
