import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { PencilLine, Plus, Trash2 } from "lucide-react";
import { useState } from "react";

import { Page } from "../../components/page";
import {
  Button,
  Panel,
  PanelBody,
  PanelHeader,
  StatusPill,
  TBody,
  TD,
  TH,
  THead,
  Table,
} from "../../components/ui";
import { api } from "../../lib/api";
import {
  NotificationChannel,
  NotificationRule,
} from "../../lib/types";

import { NotificationsTabs } from "./NotificationsTabs";
import { RuleForm } from "./RuleForm";

// /notifications — admin-only listing of notification rules. Permission gating
// is handled by App.tsx (RequireAdmin); this page assumes the caller is an
// admin.

function severityStatus(s: NotificationRule["severity"]): "ok" | "warn" | "fail" {
  if (s === "info") return "ok";
  if (s === "warning") return "warn";
  return "fail";
}

export function RulesPage() {
  const qc = useQueryClient();
  const rules = useQuery({
    queryKey: ["rules"],
    queryFn: () => api<{ rules: NotificationRule[] }>("/v1/notifications/rules"),
  });
  const channels = useQuery({
    queryKey: ["channels"],
    queryFn: () => api<{ channels: NotificationChannel[] }>("/v1/notifications/channels"),
  });

  const [editing, setEditing] = useState<NotificationRule | null>(null);
  const [creating, setCreating] = useState(false);

  const del = useMutation({
    mutationFn: (id: string) => api(`/v1/notifications/rules/${id}`, { method: "DELETE" }),
    onSuccess: () => qc.invalidateQueries({ queryKey: ["rules"] }),
  });

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
          channels={channels.data?.channels ?? []}
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
            disabled={(channels.data?.channels.length ?? 0) === 0}
            onClick={() => setCreating(true)}
          >
            <Plus className="h-3.5 w-3.5" /> New rule
          </Button>
        </PanelHeader>
        <PanelBody className="p-0 overflow-x-auto">
          {rules.isLoading ? (
            <p className="px-5 py-4 text-sm text-fg-subtle">Loading…</p>
          ) : (rules.data?.rules ?? []).length === 0 ? (
            <p className="px-5 py-8 text-center text-sm text-fg-subtle">
              {(channels.data?.channels.length ?? 0) === 0
                ? "Create a channel first, then add rules."
                : "No rules yet."}
            </p>
          ) : (
            <Table>
              <THead>
                <tr>
                  <TH>Name</TH>
                  <TH>Condition</TH>
                  <TH>Severity</TH>
                  <TH>Channels</TH>
                  <TH>Throttle</TH>
                  <TH>Enabled</TH>
                  <TH className="text-right">Actions</TH>
                </tr>
              </THead>
              <TBody>
                {(rules.data?.rules ?? []).map((r) => {
                  const chNames = r.channel_ids.map(
                    (id) => channels.data?.channels.find((c) => c.id === id)?.name ?? id.slice(0, 8),
                  );
                  return (
                    <tr key={r.id} className="hover:bg-panel-2">
                      <TD className="font-medium">{r.name}</TD>
                      <TD className="text-fg-muted">{r.condition_type}</TD>
                      <TD>
                        <StatusPill status={severityStatus(r.severity)}>{r.severity}</StatusPill>
                      </TD>
                      <TD className="text-fg-muted text-xs">{chNames.join(", ")}</TD>
                      <TD className="tabular-nums text-fg-muted">{r.throttle_sec}s</TD>
                      <TD>
                        <StatusPill status={r.enabled ? "ok" : "offline"}>
                          {r.enabled ? "on" : "off"}
                        </StatusPill>
                      </TD>
                      <TD className="text-right">
                        <div className="inline-flex items-center gap-1">
                          <Button onClick={() => setEditing(r)}>
                            <PencilLine className="h-3.5 w-3.5" /> Edit
                          </Button>
                          <Button
                            variant="danger"
                            onClick={() => {
                              if (confirm(`Delete rule "${r.name}"?`)) del.mutate(r.id);
                            }}
                          >
                            <Trash2 className="h-3.5 w-3.5" />
                          </Button>
                        </div>
                      </TD>
                    </tr>
                  );
                })}
              </TBody>
            </Table>
          )}
        </PanelBody>
      </Panel>
    </Page>
  );
}
