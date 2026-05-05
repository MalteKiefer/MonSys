import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { Tag, Trash2 } from "lucide-react";
import { Link } from "react-router-dom";

import {
  Button,
  Empty,
  ErrorBox,
  Panel,
  PanelBody,
  PanelHeader,
  StatusPill,
  TBody,
  TD,
  TH,
  THead,
  Table,
} from "../components/ui";
import { api } from "../lib/api";
import { AgentEnrollment, HostGroup } from "../lib/types";

// Polished admin view of recent agent self-enrollment tokens. The list is
// short-lived by design (server prunes after 24h) and the page exists mostly
// to give admins a way to revoke a still-pending token they minted by mistake
// or to confirm "did the agent claim its token yet?" without grepping logs.
//
// Three lifecycle states are derived client-side from used_at / expires_at
// because the backend doesn't return a status enum — keeping the type narrow.

type ListResponse = { enrollments: AgentEnrollment[] };

type Lifecycle = "consumed" | "expired" | "pending";

function lifecycle(e: AgentEnrollment): Lifecycle {
  if (e.used_at) return "consumed";
  if (e.expires_at && new Date(e.expires_at).getTime() < Date.now()) return "expired";
  return "pending";
}

export default function AdminEnrollments() {
  const qc = useQueryClient();

  const list = useQuery({
    queryKey: ["admin-enrollments"],
    queryFn: () => api<ListResponse>("/v1/admin/agents/enrollments?limit=200"),
    // 5s polling so a freshly-consumed token visibly flips from pending →
    // consumed without forcing the admin to refresh. The backend list is
    // already capped to a 24h window so the response stays small.
    refetchInterval: 5_000,
  });

  // Group lookup so the "Groups" column can show a real name on hover even
  // though the count is what's rendered. Fetched in parallel; we don't block
  // on it — if the request fails we simply show the raw count.
  const groups = useQuery({
    queryKey: ["groups"],
    queryFn: () => api<{ groups: HostGroup[] }>("/v1/groups"),
  });

  const groupNameById = (() => {
    const m = new Map<string, string>();
    for (const g of groups.data?.groups ?? []) m.set(g.id, g.name);
    return m;
  })();

  const revoke = useMutation({
    mutationFn: (id: string) =>
      api<unknown>(`/v1/admin/agents/enrollments/${id}`, { method: "DELETE" }),
    onSuccess: () => qc.invalidateQueries({ queryKey: ["admin-enrollments"] }),
  });

  const enrollments = list.data?.enrollments ?? [];

  return (
    <div className="mx-auto max-w-6xl space-y-5 p-6">
      <header>
        <h2 className="text-lg font-semibold tracking-tight">Agent enrollments</h2>
        <p className="text-sm text-fg-muted">
          One-shot tokens for <code className="font-mono">/v1/agents/install.sh</code> — pending
          tokens can be revoked, consumed and expired ones are read-only.
        </p>
      </header>

      <Panel>
        <PanelHeader>
          <h3 className="text-sm font-semibold">Recent enrollments</h3>
          <span className="text-xs text-fg-subtle tabular-nums">{enrollments.length} entries</span>
        </PanelHeader>
        <PanelBody className="p-0 overflow-x-auto">
          {list.isLoading ? (
            <p className="px-5 py-4 text-sm text-fg-subtle">Loading…</p>
          ) : list.error ? (
            <div className="px-5 py-4">
              <ErrorBox>{(list.error as Error).message}</ErrorBox>
            </div>
          ) : enrollments.length === 0 ? (
            <Empty>No enrollments in the last 24 hours.</Empty>
          ) : (
            <Table aria-label="Recent agent enrollments">
              <THead>
                <tr>
                  <TH>Created</TH>
                  <TH>Label</TH>
                  <TH>Tags</TH>
                  <TH>Groups</TH>
                  <TH>Status</TH>
                  <TH>Used by</TH>
                  <TH>Created by</TH>
                  <TH>Expires</TH>
                  <TH className="text-right">Actions</TH>
                </tr>
              </THead>
              <TBody>
                {enrollments.map((e) => (
                  <EnrollmentRow
                    key={e.id}
                    enrollment={e}
                    groupNameById={groupNameById}
                    onRevoke={(id) => {
                      if (window.confirm("Revoke this enrollment?")) revoke.mutate(id);
                    }}
                    revoking={revoke.isPending && revoke.variables === e.id}
                  />
                ))}
              </TBody>
            </Table>
          )}
        </PanelBody>
      </Panel>
    </div>
  );
}

function EnrollmentRow({
  enrollment,
  groupNameById,
  onRevoke,
  revoking,
}: {
  enrollment: AgentEnrollment;
  groupNameById: Map<string, string>;
  onRevoke: (id: string) => void;
  revoking: boolean;
}) {
  const state = lifecycle(enrollment);
  const groupCount = enrollment.group_ids?.length ?? 0;
  const groupTitle = enrollment.group_ids
    .map((id) => groupNameById.get(id) ?? id)
    .join(", ");

  return (
    <tr className="hover:bg-panel-2 align-top">
      <TD className="font-mono text-[11px] text-fg-muted whitespace-nowrap">
        {new Date(enrollment.created_at).toLocaleTimeString()}
        <span className="ml-2 text-fg-subtle">
          {new Date(enrollment.created_at).toLocaleDateString()}
        </span>
      </TD>
      <TD className="font-medium">{enrollment.label || "—"}</TD>
      <TD>
        {(enrollment.tags ?? []).length === 0 ? (
          <span className="text-fg-subtle">—</span>
        ) : (
          <div className="flex flex-wrap items-center gap-1">
            {(enrollment.tags ?? []).map((t) => (
              <span
                key={t}
                className="inline-flex items-center gap-1 rounded-md bg-panel-2 px-1.5 py-0.5 font-mono text-[10px] text-accent"
              >
                <Tag className="h-3 w-3" aria-hidden />
                {t}
              </span>
            ))}
          </div>
        )}
      </TD>
      <TD className="tabular-nums text-fg-muted">
        {groupCount === 0 ? (
          <span className="text-fg-subtle">—</span>
        ) : (
          <Link
            to="/admin/groups"
            title={groupTitle}
            className="text-info hover:underline"
          >
            {groupCount}
          </Link>
        )}
      </TD>
      <TD>
        <LifecyclePill state={state} />
      </TD>
      <TD className="text-fg-muted">
        {enrollment.used_by_host_id ? (
          <Link
            to={`/hosts/${enrollment.used_by_host_id}`}
            className="text-accent hover:underline"
          >
            {enrollment.used_by_hostname || enrollment.used_by_host_id}
          </Link>
        ) : (
          <span className="text-fg-subtle">—</span>
        )}
      </TD>
      <TD className="font-mono text-xs text-fg-muted">{enrollment.created_by || "—"}</TD>
      <TD className="font-mono text-[11px] text-fg-muted whitespace-nowrap">
        {enrollment.expires_at
          ? new Date(enrollment.expires_at).toLocaleString()
          : "—"}
      </TD>
      <TD className="text-right">
        {state === "pending" ? (
          <Button
            variant="danger"
            disabled={revoking}
            onClick={() => onRevoke(enrollment.id)}
          >
            <Trash2 className="h-3.5 w-3.5" />
            {revoking ? "Revoking…" : "Revoke"}
          </Button>
        ) : (
          <span className="text-fg-subtle">—</span>
        )}
      </TD>
    </tr>
  );
}

function LifecyclePill({ state }: { state: Lifecycle }) {
  if (state === "consumed") return <StatusPill status="ok">consumed</StatusPill>;
  if (state === "expired") return <StatusPill status="offline">expired</StatusPill>;
  return <StatusPill status="warn">pending</StatusPill>;
}
