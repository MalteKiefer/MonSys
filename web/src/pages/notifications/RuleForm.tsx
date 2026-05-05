import { useMutation, useQuery } from "@tanstack/react-query";
import {
  Bell,
  ChevronDown,
  ChevronRight,
  Code2,
  Mail,
  MessageCircle,
  MessageSquare,
  Server,
  Users,
} from "lucide-react";
import { FormEvent, useMemo, useState } from "react";

import {
  Button,
  ErrorBox,
  Field,
  Panel,
  PanelBody,
  PanelHeader,
  TextInput,
} from "../../components/ui";
import {
  CheckboxGrid,
  FormSection,
  PillGroup,
  TagChip,
  Toggle,
} from "../../components/notifications/FormParts";
import { api, ApiError } from "../../lib/api";
import { hostDisplay } from "../../lib/utils";
import {
  ChannelType,
  Host,
  HostGroup,
  NotificationChannel,
  NotificationRule,
  NotificationRuleInput,
} from "../../lib/types";

// ---- Rule form ------------------------------------------------------------
//
// Extracted verbatim from the original AdminNotifications monolith. Same
// props (`initial`, `channels`, `onCancel`, `onSaved`) so this drops in
// behind the existing `RulesPage` rendering.

const RULE_CONDITIONS: Array<{ value: NotificationRule["condition_type"]; label: string; desc: string }> = [
  { value: "host_offline", label: "Host offline / stale", desc: "Fires on host_status transitions. Param: match_states (default ['offline'])." },
  { value: "monitor_failed", label: "Monitor failed", desc: "Fires when an active monitor returns status=fail. Optional params: monitor_type, monitor_name." },
  { value: "cert_expiring", label: "Cert expiring", desc: "Fires when a cert monitor returns warn or fail." },
  { value: "login_failed_threshold", label: "Failed logins threshold", desc: "Periodic check. Params: threshold (default 10), window_sec (default 300)." },
  { value: "security_updates_pending", label: "Security updates pending", desc: "Periodic. Param: threshold (default 1)." },
];

// Per-condition placeholder for the advanced params textarea. Mirrors the
// docstring on each RULE_CONDITIONS entry; shown only as a hint, never
// pre-filled into state so an empty body still serialises as `{}`.
const CONDITION_PARAM_EXAMPLES: Record<NotificationRule["condition_type"], string> = {
  host_offline: '{\n  "match_states": ["offline", "stale"]\n}',
  monitor_failed: '{\n  "monitor_type": "http",\n  "monitor_name": "api"\n}',
  cert_expiring: "{}",
  login_failed_threshold: '{\n  "threshold": 10,\n  "window_sec": 300\n}',
  security_updates_pending: '{\n  "threshold": 1\n}',
};

// Lucide icon picker for channel types. Keep in sync with CHANNEL_TYPE_CARDS
// in ChannelForm.tsx.
function channelIcon(type: ChannelType) {
  switch (type) {
    case "email":
      return Mail;
    case "slack":
      return MessageSquare;
    case "mattermost":
    case "discord":
      return MessageCircle;
    case "ntfy":
      return Bell;
    default:
      return Bell;
  }
}

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

  const [name, setName] = useState(initial?.name ?? "");
  const [enabled, setEnabled] = useState(initial?.enabled ?? true);
  const [conditionType, setConditionType] = useState<NotificationRule["condition_type"]>(
    initial?.condition_type ?? "host_offline",
  );
  const [severity, setSeverity] = useState<NotificationRule["severity"]>(initial?.severity ?? "warning");
  const [throttleSec, setThrottleSec] = useState(initial?.throttle_sec ?? 300);
  const [repeatIntervalSec, setRepeatIntervalSec] = useState(initial?.repeat_interval_sec ?? 0);
  const [notifyOnResolve, setNotifyOnResolve] = useState(initial?.notify_on_resolve ?? true);
  const [tagsRaw, setTagsRaw] = useState((initial?.target_tags ?? []).join(", "));
  const [groupIDs, setGroupIDs] = useState<string[]>(initial?.target_group_ids ?? []);
  const [hostIDs, setHostIDs] = useState<string[]>(initial?.target_host_ids ?? []);
  const [paramsRaw, setParamsRaw] = useState(
    JSON.stringify(initial?.condition_params ?? {}, null, 2),
  );
  // Inline JSON-validation hint. Re-parsed on every keystroke so the user
  // sees the error before submitting; the server still validates the shape
  // on save, so we deliberately do not gate the submit button.
  const paramsParseError = useMemo<string | null>(() => {
    if (paramsRaw.trim() === "") return null;
    try {
      JSON.parse(paramsRaw);
      return null;
    } catch (e) {
      return (e as Error).message;
    }
  }, [paramsRaw]);
  const [channelIDs, setChannelIDs] = useState<string[]>(initial?.channel_ids ?? []);
  const [error, setError] = useState<string | null>(null);

  const save = useMutation({
    mutationFn: () => {
      let params: Record<string, unknown> = {};
      try {
        params = paramsRaw.trim() === "" ? {} : JSON.parse(paramsRaw);
      } catch (e) {
        throw new Error(`condition_params is not valid JSON: ${(e as Error).message}`);
      }
      if (channelIDs.length === 0) {
        throw new Error("Pick at least one channel");
      }
      if (repeatIntervalSec !== 0 && (repeatIntervalSec < 60 || repeatIntervalSec > 86400)) {
        throw new Error("Repeat interval must be 0 or between 60 and 86400 seconds");
      }
      const tagList = tagsRaw
        .split(",")
        .map((s) => s.trim().toLowerCase())
        .filter(Boolean);
      const body: NotificationRuleInput = {
        name,
        enabled,
        condition_type: conditionType,
        condition_params: params,
        channel_ids: channelIDs,
        severity,
        throttle_sec: throttleSec,
        repeat_interval_sec: repeatIntervalSec,
        notify_on_resolve: notifyOnResolve,
        target_host_ids: hostIDs,
        target_tags: tagList,
        target_group_ids: groupIDs,
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

  function submit(e: FormEvent) {
    e.preventDefault();
    setError(null);
    save.mutate();
  }

  const conditionMeta = RULE_CONDITIONS.find((c) => c.value === conditionType)!;
  const conditionExample = CONDITION_PARAM_EXAMPLES[conditionType];

  // Tag-chip rendering: parse the comma-separated free-text input into chips
  // for visual feedback. The raw text remains the source of truth so the user
  // can keep typing mid-tag without losing input focus.
  const tagChips = useMemo(
    () =>
      tagsRaw
        .split(",")
        .map((s) => s.trim().toLowerCase())
        .filter(Boolean),
    [tagsRaw],
  );
  function removeTag(tag: string) {
    const next = tagChips.filter((t) => t !== tag);
    setTagsRaw(next.join(", "));
  }

  const channelOptions = channels.map((c) => ({
    id: c.id,
    primary: c.name,
    secondary: c.type,
    icon: channelIcon(c.type),
  }));

  const hostOptions = (hostsQuery.data?.hosts ?? []).map((h) => ({
    id: h.id,
    primary: hostDisplay(h),
    secondary:
      (h.tags ?? []).length > 0 ? (
        <span className="inline-flex flex-wrap gap-1">
          {(h.tags ?? []).map((t) => (
            <span
              key={t}
              className="rounded-md bg-panel-2 px-1 font-mono text-[10px] text-accent"
            >
              #{t}
            </span>
          ))}
        </span>
      ) : (
        <span className="text-fg-subtle">no tags</span>
      ),
    icon: Server,
  }));

  const groupOptions = (groupsQuery.data?.groups ?? []).map((g) => ({
    id: g.id,
    primary: g.name,
    secondary: `${g.member_ids.length} member${g.member_ids.length === 1 ? "" : "s"}`,
    icon: Users,
  }));

  return (
    <Panel>
      <PanelHeader>
        <h3 className="text-sm font-semibold">{initial ? `Edit ${initial.name}` : "New rule"}</h3>
      </PanelHeader>
      <PanelBody>
        <form onSubmit={submit} className="space-y-5">
          {/* --- Identification --- */}
          <FormSection label="Identification">
            <div className="grid grid-cols-1 gap-3 md:grid-cols-[1fr_auto] md:items-end">
              <Field label="Name">
                <TextInput
                  required
                  value={name}
                  onChange={(e) => setName(e.target.value)}
                  placeholder="e.g. prod hosts offline"
                />
              </Field>
              <Field label="Severity">
                <PillGroup
                  value={severity}
                  onChange={(v) => setSeverity(v)}
                  label="Severity"
                  options={[
                    {
                      value: "info",
                      label: "info",
                      activeClass: "bg-ok/15 text-ok ring-ok/30",
                    },
                    {
                      value: "warning",
                      label: "warning",
                      activeClass: "bg-warn/15 text-warn ring-warn/30",
                    },
                    {
                      value: "critical",
                      label: "critical",
                      activeClass: "bg-fail/15 text-fail ring-fail/30",
                    },
                  ]}
                />
              </Field>
            </div>
          </FormSection>

          {/* --- Trigger --- */}
          <FormSection label="Trigger">
            <Field label="Condition" hint={conditionMeta.desc}>
              <select
                value={conditionType}
                onChange={(e) => {
                  const next = e.target.value as NotificationRule["condition_type"];
                  setConditionType(next);
                  // If the user hasn't customised params, refresh the placeholder
                  // hint to match the new condition.
                  if (paramsRaw.trim() === "" || paramsRaw.trim() === "{}") {
                    setParamsRaw("");
                  }
                }}
                className="w-full rounded-md border border-border bg-panel px-3 py-2 text-sm focus:border-accent focus:outline-none"
              >
                {RULE_CONDITIONS.map((c) => (
                  <option key={c.value} value={c.value}>
                    {c.label}
                  </option>
                ))}
              </select>
            </Field>

            <AdvancedParams
              value={paramsRaw}
              onChange={setParamsRaw}
              placeholder={conditionExample}
              parseError={paramsParseError}
            />
          </FormSection>

          {/* --- Targets --- */}
          <FormSection
            label="Targets"
            hint="Empty = every host. Otherwise host must match any selection."
          >
            <Field label={`Hosts (${hostIDs.length} selected)`}>
              <CheckboxGrid
                options={hostOptions}
                selected={hostIDs}
                onToggle={(id) =>
                  setHostIDs((prev) =>
                    prev.includes(id) ? prev.filter((x) => x !== id) : [...prev, id],
                  )
                }
                empty="No hosts known yet."
                maxHeight="max-h-56"
              />
            </Field>

            <div className="grid grid-cols-1 gap-3 md:grid-cols-2">
              <Field
                label="Tags"
                hint={
                  tagsQuery.data?.tags?.length
                    ? `Existing: ${tagsQuery.data.tags.map((t) => t.tag).join(", ")}`
                    : "No tags defined yet."
                }
              >
                <TextInput
                  value={tagsRaw}
                  onChange={(e) => setTagsRaw(e.target.value)}
                  placeholder="prod, db"
                  className="font-mono"
                />
                {tagChips.length > 0 && (
                  <div className="mt-2 flex flex-wrap gap-1">
                    {tagChips.map((t) => (
                      <TagChip key={t} text={t} onRemove={() => removeTag(t)} />
                    ))}
                  </div>
                )}
              </Field>

              <Field label={`Groups (${groupIDs.length} selected)`}>
                <CheckboxGrid
                  options={groupOptions}
                  selected={groupIDs}
                  onToggle={(id) =>
                    setGroupIDs((prev) =>
                      prev.includes(id) ? prev.filter((x) => x !== id) : [...prev, id],
                    )
                  }
                  empty="No groups defined yet."
                  maxHeight="max-h-40"
                  columns="grid-cols-1"
                />
              </Field>
            </div>
          </FormSection>

          {/* --- Delivery --- */}
          <FormSection label="Delivery">
            <Field
              label={`Channels (${channelIDs.length} selected)`}
              hint="At least one channel must be selected."
            >
              <CheckboxGrid
                options={channelOptions}
                selected={channelIDs}
                onToggle={(id) =>
                  setChannelIDs((prev) =>
                    prev.includes(id) ? prev.filter((x) => x !== id) : [...prev, id],
                  )
                }
                empty="No channels available."
                maxHeight="max-h-56"
              />
            </Field>
          </FormSection>

          {/* --- Cadence --- */}
          <FormSection label="Cadence" divider={false}>
            <div className="grid grid-cols-1 gap-3 md:grid-cols-2">
              <Field label="Throttle (seconds)" hint="0 disables throttle.">
                <TextInput
                  type="number"
                  min={0}
                  value={throttleSec}
                  onChange={(e) => setThrottleSec(parseInt(e.target.value || "0", 10))}
                />
              </Field>
              <Field
                label="Repeat reminder (seconds)"
                hint="0 = fire once per outage. 60–86400 = re-send while still active."
              >
                <TextInput
                  type="number"
                  min={0}
                  max={86400}
                  value={repeatIntervalSec}
                  onChange={(e) => setRepeatIntervalSec(parseInt(e.target.value || "0", 10))}
                />
              </Field>
            </div>
            <div className="grid grid-cols-1 gap-3 rounded-md border border-border bg-panel-2/40 p-3 md:grid-cols-2">
              <Toggle
                checked={notifyOnResolve}
                onChange={setNotifyOnResolve}
                label="Notify on resolve"
                hint="Send an all-clear when the host or monitor recovers."
              />
              <Toggle
                checked={enabled}
                onChange={setEnabled}
                label="Enabled"
                hint="Disable to keep the rule but stop firing alerts."
              />
            </div>
          </FormSection>

          {error && <ErrorBox>{error}</ErrorBox>}

          <div className="flex items-center gap-2 pt-1">
            <Button variant="primary" type="submit" disabled={save.isPending}>
              {save.isPending ? "Saving…" : initial ? "Save" : "Create"}
            </Button>
            <Button type="button" onClick={onCancel}>
              Cancel
            </Button>
          </div>
        </form>
      </PanelBody>
    </Panel>
  );
}

// Collapsible "Advanced" expander housing the JSON params textarea.
// Closed by default for new rules; auto-opens when editing a rule that has
// non-empty params (so the user can see what's there) or when the JSON fails
// to parse (so the inline error isn't hidden behind a closed pane).
function AdvancedParams({
  value,
  onChange,
  placeholder,
  parseError,
}: {
  value: string;
  onChange: (v: string) => void;
  placeholder: string;
  parseError: string | null;
}) {
  const hasContent = value.trim() !== "" && value.trim() !== "{}";
  const [open, setOpen] = useState<boolean>(hasContent || !!parseError);

  return (
    <div className="rounded-md border border-border bg-panel-2/40">
      <button
        type="button"
        onClick={() => setOpen((o) => !o)}
        className="flex w-full items-center justify-between gap-2 px-3 py-2 text-left text-xs font-medium text-fg-muted hover:text-fg"
      >
        <span className="inline-flex items-center gap-2">
          <Code2 className="h-3.5 w-3.5" />
          Advanced — condition params (JSON)
          {hasContent && (
            <span className="rounded bg-accent/15 px-1.5 py-0.5 font-mono text-[10px] text-accent">
              custom
            </span>
          )}
          {parseError && (
            <span className="rounded bg-fail/15 px-1.5 py-0.5 text-[10px] text-fail">
              invalid
            </span>
          )}
        </span>
        {open ? (
          <ChevronDown className="h-3.5 w-3.5 text-fg-subtle" />
        ) : (
          <ChevronRight className="h-3.5 w-3.5 text-fg-subtle" />
        )}
      </button>
      {open && (
        <div className="border-t border-border px-3 pb-3 pt-2">
          <textarea
            rows={5}
            value={value}
            onChange={(e) => onChange(e.target.value)}
            placeholder={placeholder}
            className="w-full rounded-md border border-border bg-panel px-3 py-2 font-mono text-xs text-fg placeholder:text-fg-subtle focus:border-accent focus:outline-none focus:ring-2 focus:ring-accent/30"
          />
          {parseError ? (
            <p className="mt-1 text-xs text-fail">Invalid JSON: {parseError}</p>
          ) : (
            <p className="mt-1 text-[11px] text-fg-subtle">
              Leave blank to use the condition defaults.
            </p>
          )}
        </div>
      )}
    </div>
  );
}
