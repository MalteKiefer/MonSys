// /notifications — admin-only listing of notification rules. Permission
// gating is handled by App.tsx (RequireAdmin); this page assumes the caller
// is an admin.
//
// Multi-condition support: rows that share a `group_id` are folded into one
// card with a bulleted list of legs. Edit / Enable-toggle / Delete actions
// apply to the whole group. Standalone rules (group_id == null) keep the
// classic row layout in a single-leg group card so the visual treatment is
// consistent.

import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { PencilLine, Plus, Power, PowerOff, Trash2 } from "lucide-react";
import { useMemo, useState } from "react";

import { Page } from "../../components/page";
import {
  Button,
  Panel,
  PanelBody,
  PanelHeader,
  StatusPill,
} from "../../components/ui";
import { api } from "../../lib/api";
import {
  NotificationChannel,
  NotificationRule,
  NotificationRuleInput,
} from "../../lib/types";

import { NotificationsTabs } from "./NotificationsTabs";
import { RuleForm } from "./RuleForm";
import {
  conditionIcon,
  conditionLabel,
  conditionSummary,
  stripConditionSuffix,
} from "./RuleWizard/catalogue";

function severityStatus(s: NotificationRule["severity"]): "ok" | "warn" | "fail" {
  if (s === "info") return "ok";
  if (s === "warning") return "warn";
  return "fail";
}

// A logical group of rule rows. Standalone rules (no group_id) become a
// single-row group keyed by their id so the rendering loop is uniform.
type RuleGroup = {
  // Stable identifier — group_id when shared, otherwise the rule id.
  key: string;
  // True only when the rows actually share a backend group_id.
  isGroup: boolean;
  // The displayed name (suffix stripped on group rows).
  name: string;
  rows: NotificationRule[];
};

function groupRules(rules: NotificationRule[]): RuleGroup[] {
  const byGroup = new Map<string, NotificationRule[]>();
  const standalone: NotificationRule[] = [];
  for (const r of rules) {
    if (r.group_id) {
      const arr = byGroup.get(r.group_id) ?? [];
      arr.push(r);
      byGroup.set(r.group_id, arr);
    } else {
      standalone.push(r);
    }
  }
  const result: RuleGroup[] = [];
  for (const [gid, rows] of byGroup) {
    // Pick the row with the shortest display name as the canonical leg —
    // when only one leg exists the suffix is omitted server-side, so all
    // rows usually agree anyway. Sort legs by condition_type for stable UI.
    const sorted = [...rows].sort((a, b) =>
      a.condition_type.localeCompare(b.condition_type),
    );
    const displayName = stripConditionSuffix(sorted[0].name);
    result.push({ key: gid, isGroup: true, name: displayName, rows: sorted });
  }
  for (const r of standalone) {
    result.push({ key: r.id, isGroup: false, name: r.name, rows: [r] });
  }
  // Sort groups by name for a predictable list.
  result.sort((a, b) => a.name.localeCompare(b.name));
  return result;
}

export function RulesPage() {
  const qc = useQueryClient();
  const rules = useQuery({
    queryKey: ["rules"],
    queryFn: () => api<{ rules: NotificationRule[] }>("/v1/notifications/rules"),
  });
  const channels = useQuery({
    queryKey: ["channels"],
    queryFn: () =>
      api<{ channels: NotificationChannel[] }>("/v1/notifications/channels"),
  });

  const [editing, setEditing] = useState<NotificationRule | null>(null);
  const [creating, setCreating] = useState(false);

  // Delete every row of a group sequentially. The backend has no atomic
  // "delete group" endpoint yet; looping in the frontend is fine because
  // the typical group has 2-5 legs.
  const delGroup = useMutation({
    mutationFn: async (rows: NotificationRule[]) => {
      for (const r of rows) {
        await api(`/v1/notifications/rules/${r.id}`, { method: "DELETE" });
      }
    },
    onSuccess: () => qc.invalidateQueries({ queryKey: ["rules"] }),
  });

  // Toggle enabled on every leg via PUT. We don't have a single endpoint for
  // this; PUT requires the full body so we round-trip each row.
  const toggleGroup = useMutation({
    mutationFn: async ({
      rows,
      enabled,
    }: {
      rows: NotificationRule[];
      enabled: boolean;
    }) => {
      for (const r of rows) {
        const body: NotificationRuleInput = {
          name: r.name,
          enabled,
          condition_type: r.condition_type,
          condition_params: r.condition_params ?? {},
          channel_ids: r.channel_ids,
          severity: r.severity,
          throttle_sec: r.throttle_sec,
          repeat_interval_sec: r.repeat_interval_sec,
          notify_on_resolve: r.notify_on_resolve,
          target_host_ids: r.target_host_ids ?? [],
          target_tags: r.target_tags ?? [],
          target_group_ids: r.target_group_ids ?? [],
        };
        await api(`/v1/notifications/rules/${r.id}`, {
          method: "PUT",
          body: JSON.stringify(body),
        });
      }
    },
    onSuccess: () => qc.invalidateQueries({ queryKey: ["rules"] }),
  });

  const allRules = rules.data?.rules ?? [];
  const groups = useMemo(() => groupRules(allRules), [allRules]);
  const allChannels = channels.data?.channels ?? [];

  return (
    <Page
      title="Notification rules"
      subtitle="Trigger alerts when host status, monitor result, or login pattern changes."
      actions={<NotificationsTabs />}
    >
      {(creating || editing) && (
        <RuleForm
          key={editing?.id ?? "new"}
          initial={editing}
          allRules={allRules}
          channels={allChannels}
          onCancel={() => {
            setEditing(null);
            setCreating(false);
          }}
          onSaved={() => {
            qc.invalidateQueries({ queryKey: ["rules"] });
            setEditing(null);
            setCreating(false);
          }}
        />
      )}

      <Panel>
        <PanelHeader>
          <h3 className="text-sm font-semibold">Rules</h3>
          <Button
            variant="primary"
            disabled={allChannels.length === 0}
            onClick={() => setCreating(true)}
          >
            <Plus className="h-3.5 w-3.5" /> New rule
          </Button>
        </PanelHeader>
        <PanelBody className="p-0">
          {rules.isLoading ? (
            <p className="px-5 py-4 text-sm text-fg-subtle">Loading…</p>
          ) : allRules.length === 0 ? (
            <p className="px-5 py-8 text-center text-sm text-fg-subtle">
              {allChannels.length === 0
                ? "Create a channel first, then add rules."
                : "No rules yet."}
            </p>
          ) : (
            <ul className="divide-y divide-border">
              {groups.map((g) => (
                <GroupCard
                  key={g.key}
                  group={g}
                  channels={allChannels}
                  onEdit={() => setEditing(g.rows[0])}
                  onToggle={(enabled) =>
                    toggleGroup.mutate({ rows: g.rows, enabled })
                  }
                  onDelete={() => {
                    const label =
                      g.isGroup && g.rows.length > 1
                        ? `Delete group "${g.name}" (${g.rows.length} rules)?`
                        : `Delete rule "${g.name}"?`;
                    if (confirm(label)) delGroup.mutate(g.rows);
                  }}
                />
              ))}
            </ul>
          )}
        </PanelBody>
      </Panel>
    </Page>
  );
}

function GroupCard({
  group,
  channels,
  onEdit,
  onToggle,
  onDelete,
}: {
  group: RuleGroup;
  channels: NotificationChannel[];
  onEdit: () => void;
  onToggle: (enabled: boolean) => void;
  onDelete: () => void;
}) {
  const head = group.rows[0];
  // All rows of a group share name/scope/channels/throttle/severity by
  // construction; if a sysadmin somehow ended up with mixed enabled states
  // we surface that explicitly via "mixed" rather than silently picking one.
  const anyEnabled = group.rows.some((r) => r.enabled);
  const allEnabled = group.rows.every((r) => r.enabled);
  const enabledMixed = anyEnabled && !allEnabled;
  const chNames = head.channel_ids.map(
    (id) => channels.find((c) => c.id === id)?.name ?? id.slice(0, 8),
  );

  return (
    <li className="px-5 py-3 hover:bg-panel-2/50">
      <div className="flex flex-wrap items-start gap-3">
        <div className="min-w-0 flex-1 space-y-2">
          <div className="flex flex-wrap items-baseline gap-2">
            <p className="text-sm font-semibold text-fg">{group.name}</p>
            {group.rows.length > 1 && (
              <span className="rounded-md bg-accent/10 px-1.5 py-0.5 text-[10px] font-medium text-accent ring-1 ring-inset ring-accent/30">
                {group.rows.length} conditions
              </span>
            )}
            <StatusPill status={severityStatus(head.severity)}>
              {head.severity}
            </StatusPill>
            <StatusPill
              status={
                enabledMixed ? "warn" : allEnabled ? "ok" : "offline"
              }
            >
              {enabledMixed ? "mixed" : allEnabled ? "on" : "off"}
            </StatusPill>
          </div>

          <ul className="space-y-1">
            {group.rows.map((r) => {
              const Icon = conditionIcon(r.condition_type);
              return (
                <li
                  key={r.id}
                  className="flex items-start gap-2 text-[12px] text-fg-muted"
                >
                  <Icon className="mt-0.5 h-3.5 w-3.5 shrink-0 text-accent" />
                  <span className="min-w-0">
                    <span className="font-medium text-fg">
                      {conditionLabel(r.condition_type)}
                    </span>
                    <span className="text-fg-subtle"> — </span>
                    <span>
                      {conditionSummary(r.condition_type, r.condition_params ?? {})}
                    </span>
                  </span>
                </li>
              );
            })}
          </ul>

          <p className="text-[11px] text-fg-subtle">
            Channels:{" "}
            <span className="text-fg-muted">
              {chNames.length > 0 ? chNames.join(", ") : "—"}
            </span>
            {" • "}Throttle: <span className="tabular-nums">{head.throttle_sec}s</span>
            {head.repeat_interval_sec > 0 && (
              <>
                {" • "}Repeat:{" "}
                <span className="tabular-nums">
                  {head.repeat_interval_sec}s
                </span>
              </>
            )}
          </p>
        </div>

        <div className="flex shrink-0 items-center gap-1">
          <Button onClick={onEdit}>
            <PencilLine className="h-3.5 w-3.5" /> Edit
          </Button>
          <Button onClick={() => onToggle(!allEnabled)}>
            {allEnabled ? (
              <>
                <PowerOff className="h-3.5 w-3.5" /> Disable
              </>
            ) : (
              <>
                <Power className="h-3.5 w-3.5" /> Enable
              </>
            )}
          </Button>
          <Button variant="danger" onClick={onDelete}>
            <Trash2 className="h-3.5 w-3.5" />
          </Button>
        </div>
      </div>
    </li>
  );
}
