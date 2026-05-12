import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import {
  Check,
  Clock,
  Copy,
  Key,
  Plus,
  Tag,
  Ticket,
  Trash2,
} from "lucide-react";
import { FormEvent, useState } from "react";
import { Link } from "react-router-dom";

import { EmptyState, ErrorState, Page } from "../components/page";
import {
  Button,
  ErrorBox,
  Field,
  Panel,
  PanelBody,
  PanelHeader,
  StatusPill,
  SuccessBox,
  TBody,
  TD,
  TH,
  THead,
  Table,
  Tabs,
  type TabItem,
  TextInput,
} from "../components/ui";
import { api, ApiError } from "../lib/api";
import {
  AgentEnrollment,
  AgentEnrollmentCreateResponse,
  AgentEnrollmentInput,
  HostGroup,
} from "../lib/types";

// Polished admin view of recent agent self-enrollment tokens. The list is
// short-lived by design (server prunes after 24h) and the page exists mostly
// to give admins a way to revoke a still-pending token they minted by mistake
// or to confirm "did the agent claim its token yet?" without grepping logs.
//
// Three lifecycle states are derived client-side from used_at / expires_at
// because the backend doesn't return a status enum — keeping the type narrow.
//
// The page is split into three tabs:
//   active   — pending (unused, unexpired) tokens with revoke + copy-ID
//   consumed — history of used + expired tokens
//   create   — form to mint a new bootstrap token (label/desc/ttl/tags/groups)

type ListResponse = { enrollments: AgentEnrollment[] };

type Lifecycle = "consumed" | "expired" | "pending";

type TabKey = "active" | "consumed" | "create";

function lifecycle(e: AgentEnrollment): Lifecycle {
  if (e.used_at) return "consumed";
  if (e.expires_at && new Date(e.expires_at).getTime() < Date.now()) return "expired";
  return "pending";
}

export default function AdminEnrollments() {
  const qc = useQueryClient();
  const [tab, setTab] = useState<TabKey>("active");

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
  const activeEnrollments = enrollments.filter((e) => lifecycle(e) === "pending");
  const consumedEnrollments = enrollments.filter((e) => lifecycle(e) !== "pending");

  const tabs: ReadonlyArray<TabItem<TabKey>> = [
    {
      key: "active",
      label: "Active tokens",
      icon: Key,
      badge: activeEnrollments.length || undefined,
    },
    {
      key: "consumed",
      label: "History",
      icon: Clock,
      badge: consumedEnrollments.length || undefined,
    },
    { key: "create", label: "Create new", icon: Plus },
  ];

  function onRevoke(id: string) {
    if (window.confirm("Revoke this enrollment?")) revoke.mutate(id);
  }

  return (
    <Page
      title="Agent enrollments"
      subtitle={
        <>
          One-shot tokens for{" "}
          <code className="font-mono">/v1/agents/install.sh</code> — pending
          tokens can be revoked.
        </>
      }
    >
      <Tabs<TabKey> items={tabs} value={tab} onChange={setTab} />

      {tab === "active" && (
        <ActiveTokensPanel
          enrollments={activeEnrollments}
          loading={list.isLoading}
          error={list.error as Error | null}
          onRetry={() => list.refetch()}
          groupNameById={groupNameById}
          onRevoke={onRevoke}
          revokingId={revoke.isPending ? (revoke.variables as string | undefined) : undefined}
        />
      )}

      {tab === "consumed" && (
        <HistoryPanel
          enrollments={consumedEnrollments}
          loading={list.isLoading}
          error={list.error as Error | null}
          onRetry={() => list.refetch()}
          groupNameById={groupNameById}
        />
      )}

      {tab === "create" && (
        <CreateTokenPanel
          onCreated={() => {
            qc.invalidateQueries({ queryKey: ["admin-enrollments"] });
          }}
          onSwitchToActive={() => setTab("active")}
        />
      )}
    </Page>
  );
}

// ---- Active tokens panel --------------------------------------------------

function ActiveTokensPanel({
  enrollments,
  loading,
  error,
  onRetry,
  groupNameById,
  onRevoke,
  revokingId,
}: {
  enrollments: AgentEnrollment[];
  loading: boolean;
  error: Error | null;
  onRetry: () => void;
  groupNameById: Map<string, string>;
  onRevoke: (id: string) => void;
  revokingId: string | undefined;
}) {
  return (
    <Panel>
      <PanelHeader>
        <h3 className="text-sm font-semibold">Active tokens</h3>
        <span className="text-xs text-fg-subtle tabular-nums">
          {enrollments.length} pending
        </span>
      </PanelHeader>
      <PanelBody className="p-0 overflow-x-auto">
        {loading ? (
          <p className="px-5 py-4 text-sm text-fg-subtle">Loading…</p>
        ) : error ? (
          <div className="p-5">
            <ErrorState message={error.message} onRetry={onRetry} />
          </div>
        ) : enrollments.length === 0 ? (
          <EmptyState
            icon={Ticket}
            title="No active enrollment tokens."
            hint="Mint a new one from the Create tab; tokens auto-expire after their TTL (max 24h)."
          />
        ) : (
          <Table aria-label="Active enrollment tokens">
            <THead>
              <tr>
                <TH>Created</TH>
                <TH>Label</TH>
                <TH>Description</TH>
                <TH>Tags</TH>
                <TH>Groups</TH>
                <TH>Created by</TH>
                <TH>Expires</TH>
                <TH className="text-right">Actions</TH>
              </tr>
            </THead>
            <TBody>
              {enrollments.map((e) => (
                <ActiveRow
                  key={e.id}
                  enrollment={e}
                  groupNameById={groupNameById}
                  onRevoke={onRevoke}
                  revoking={revokingId === e.id}
                />
              ))}
            </TBody>
          </Table>
        )}
      </PanelBody>
    </Panel>
  );
}

function ActiveRow({
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
  const [copied, setCopied] = useState(false);

  async function copyId() {
    try {
      await navigator.clipboard.writeText(enrollment.id);
      setCopied(true);
      setTimeout(() => setCopied(false), 1500);
    } catch {
      /* clipboard may be unavailable on insecure origins; ignore */
    }
  }

  return (
    <tr className="hover:bg-panel-2 align-top">
      <TD className="font-mono text-[11px] text-fg-muted whitespace-nowrap">
        {new Date(enrollment.created_at).toLocaleTimeString()}
        <span className="ml-2 text-fg-subtle">
          {new Date(enrollment.created_at).toLocaleDateString()}
        </span>
      </TD>
      <TD className="font-medium">{enrollment.label || "—"}</TD>
      <TD className="text-fg-muted">
        {enrollment.description ? (
          <span
            className="block max-w-[24ch] truncate"
            title={enrollment.description}
          >
            {enrollment.description}
          </span>
        ) : (
          <span className="text-fg-subtle">—</span>
        )}
      </TD>
      <TD>
        <TagList tags={enrollment.tags ?? []} />
      </TD>
      <TD className="tabular-nums text-fg-muted">
        <GroupCount ids={enrollment.group_ids} nameById={groupNameById} />
      </TD>
      <TD className="font-mono text-xs text-fg-muted">
        {enrollment.created_by || "—"}
      </TD>
      <TD className="font-mono text-[11px] text-fg-muted whitespace-nowrap">
        {enrollment.expires_at
          ? new Date(enrollment.expires_at).toLocaleString()
          : "—"}
      </TD>
      <TD className="text-right">
        <div className="inline-flex items-center justify-end gap-1.5">
          <Button
            size="sm"
            onClick={copyId}
            aria-label="Copy enrollment ID"
            title="Copy enrollment ID"
          >
            {copied ? (
              <>
                <Check className="h-3.5 w-3.5" /> Copied
              </>
            ) : (
              <>
                <Copy className="h-3.5 w-3.5" /> Copy ID
              </>
            )}
          </Button>
          <Button
            size="sm"
            variant="danger"
            disabled={revoking}
            onClick={() => onRevoke(enrollment.id)}
          >
            <Trash2 className="h-3.5 w-3.5" />
            {revoking ? "Revoking…" : "Revoke"}
          </Button>
        </div>
      </TD>
    </tr>
  );
}

// ---- History panel --------------------------------------------------------

function HistoryPanel({
  enrollments,
  loading,
  error,
  onRetry,
  groupNameById,
}: {
  enrollments: AgentEnrollment[];
  loading: boolean;
  error: Error | null;
  onRetry: () => void;
  groupNameById: Map<string, string>;
}) {
  return (
    <Panel>
      <PanelHeader>
        <h3 className="text-sm font-semibold">History</h3>
        <span className="text-xs text-fg-subtle tabular-nums">
          {enrollments.length} entries
        </span>
      </PanelHeader>
      <PanelBody className="p-0 overflow-x-auto">
        {loading ? (
          <p className="px-5 py-4 text-sm text-fg-subtle">Loading…</p>
        ) : error ? (
          <div className="p-5">
            <ErrorState message={error.message} onRetry={onRetry} />
          </div>
        ) : enrollments.length === 0 ? (
          <EmptyState
            icon={Clock}
            title="No consumed or expired tokens in the last 24 hours."
            hint="Issued tokens auto-expire after their TTL; the server prunes records older than 24h."
          />
        ) : (
          <Table aria-label="Consumed enrollment tokens">
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
              </tr>
            </THead>
            <TBody>
              {enrollments.map((e) => (
                <HistoryRow
                  key={e.id}
                  enrollment={e}
                  groupNameById={groupNameById}
                />
              ))}
            </TBody>
          </Table>
        )}
      </PanelBody>
    </Panel>
  );
}

function HistoryRow({
  enrollment,
  groupNameById,
}: {
  enrollment: AgentEnrollment;
  groupNameById: Map<string, string>;
}) {
  const state = lifecycle(enrollment);
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
        <TagList tags={enrollment.tags ?? []} />
      </TD>
      <TD className="tabular-nums text-fg-muted">
        <GroupCount ids={enrollment.group_ids} nameById={groupNameById} />
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
      <TD className="font-mono text-xs text-fg-muted">
        {enrollment.created_by || "—"}
      </TD>
      <TD className="font-mono text-[11px] text-fg-muted whitespace-nowrap">
        {enrollment.expires_at
          ? new Date(enrollment.expires_at).toLocaleString()
          : "—"}
      </TD>
    </tr>
  );
}

// ---- Create new panel -----------------------------------------------------

function CreateTokenPanel({
  onCreated,
  onSwitchToActive,
}: {
  onCreated: () => void;
  onSwitchToActive: () => void;
}) {
  const tagsQuery = useQuery({
    queryKey: ["tags"],
    queryFn: () => api<{ tags: Array<{ tag: string; count: number }> }>("/v1/tags"),
  });
  const groupsQuery = useQuery({
    queryKey: ["groups"],
    queryFn: () => api<{ groups: HostGroup[] }>("/v1/groups"),
  });

  const [label, setLabel] = useState("");
  const [description, setDescription] = useState("");
  const [tagsRaw, setTagsRaw] = useState("");
  const [groupIDs, setGroupIDs] = useState<string[]>([]);
  const [ttlMinutes, setTTLMinutes] = useState(30);
  const [error, setError] = useState<string | null>(null);
  const [created, setCreated] = useState<AgentEnrollmentCreateResponse | null>(null);
  const [copiedCmd, setCopiedCmd] = useState(false);
  const [copiedURL, setCopiedURL] = useState(false);

  const create = useMutation({
    mutationFn: () => {
      if (ttlMinutes < 5 || ttlMinutes > 1440) {
        throw new Error("TTL must be between 5 and 1440 minutes.");
      }
      const tags = tagsRaw
        .split(",")
        .map((s) => s.trim().toLowerCase())
        .filter(Boolean);
      const body: AgentEnrollmentInput = {
        label: label.trim() || undefined,
        description: description.trim() || undefined,
        tags: tags.length ? tags : undefined,
        group_ids: groupIDs.length ? groupIDs : undefined,
        ttl_minutes: ttlMinutes,
      };
      return api<AgentEnrollmentCreateResponse>("/v1/admin/agents/enrollments", {
        method: "POST",
        body: JSON.stringify(body),
      });
    },
    onSuccess: (r) => {
      setCreated(r);
      onCreated();
    },
    onError: (err) => setError(err instanceof ApiError ? err.detail : (err as Error).message),
  });

  function submit(e: FormEvent) {
    e.preventDefault();
    setError(null);
    create.mutate();
  }

  function resetForm() {
    setLabel("");
    setDescription("");
    setTagsRaw("");
    setGroupIDs([]);
    setTTLMinutes(30);
    setError(null);
    setCreated(null);
  }

  async function copyText(text: string, setFlag: (b: boolean) => void) {
    try {
      await navigator.clipboard.writeText(text);
      setFlag(true);
      setTimeout(() => setFlag(false), 1500);
    } catch {
      /* clipboard may be unavailable on insecure origins; ignore */
    }
  }

  return (
    <Panel>
      <PanelHeader>
        <h3 className="text-sm font-semibold">Create new bootstrap token</h3>
      </PanelHeader>
      <PanelBody>
        {created ? (
          <div className="space-y-4">
            <SuccessBox>
              Token generated. Shown once — copy the install command now.
            </SuccessBox>

            <Field label="Install command" hint="Run on the new host as root.">
              <div className="flex items-start justify-between gap-3 rounded-md border border-border bg-bg/60 px-3 py-2">
                <pre className="m-0 flex-1 whitespace-pre-wrap break-all font-mono text-xs text-fg">
                  {created.install_command}
                </pre>
                <div className="flex shrink-0 flex-col gap-1.5 sm:flex-row">
                  <Button
                    variant="primary"
                    size="sm"
                    onClick={() => copyText(created.install_command, setCopiedCmd)}
                    aria-label="Copy install command"
                  >
                    {copiedCmd ? (
                      <>
                        <Check className="h-3.5 w-3.5" /> Copied
                      </>
                    ) : (
                      <>
                        <Copy className="h-3.5 w-3.5" /> Copy command
                      </>
                    )}
                  </Button>
                  <Button
                    size="sm"
                    onClick={() => copyText(created.install_url, setCopiedURL)}
                    aria-label="Copy install URL"
                  >
                    {copiedURL ? (
                      <>
                        <Check className="h-3.5 w-3.5" /> Copied
                      </>
                    ) : (
                      <>
                        <Copy className="h-3.5 w-3.5" /> Copy URL
                      </>
                    )}
                  </Button>
                </div>
              </div>
            </Field>

            <div className="flex items-center justify-end gap-2 pt-2">
              <Button onClick={resetForm}>Mint another</Button>
              <Button variant="primary" onClick={onSwitchToActive}>
                View active tokens
              </Button>
            </div>
          </div>
        ) : (
          <form onSubmit={submit} className="space-y-4">
            <p className="text-xs text-fg-subtle">
              Generates a single-use enrollment token. The new agent claims it on its
              first check-in and inherits the label, tags, and groups you set here.
            </p>

            <div className="grid grid-cols-1 gap-3 sm:grid-cols-2">
              <Field
                label="Display label"
                hint="Optional. Shown in the host list before the first hostname is reported."
              >
                <TextInput
                  value={label}
                  onChange={(e) => setLabel(e.target.value)}
                  placeholder="e.g. db-replica-3"
                  maxLength={120}
                />
              </Field>
              <Field label="Token TTL (minutes)" hint="Min 5, max 1440 (24h). Default 30.">
                <TextInput
                  type="number"
                  min={5}
                  max={1440}
                  value={ttlMinutes}
                  onChange={(e) => setTTLMinutes(parseInt(e.target.value || "0", 10))}
                />
              </Field>
            </div>

            <Field label="Description" hint={`Optional, max 200 chars. (${description.length}/200)`}>
              <TextInput
                value={description}
                onChange={(e) => setDescription(e.target.value.slice(0, 200))}
                placeholder="Why this host is being added"
                maxLength={200}
              />
            </Field>

            <Field
              label="Default tags (comma-separated)"
              hint={
                tagsQuery.data?.tags?.length
                  ? `Existing: ${tagsQuery.data.tags
                      .slice(0, 12)
                      .map((t) => t.tag)
                      .join(", ")}`
                  : "No tags defined yet."
              }
            >
              <TextInput
                value={tagsRaw}
                onChange={(e) => setTagsRaw(e.target.value)}
                placeholder="prod, db"
                className="font-mono"
              />
            </Field>

            <Field label="Default groups (Ctrl/⌘ to multi-select)">
              <select
                multiple
                size={Math.min(5, Math.max(2, groupsQuery.data?.groups.length ?? 2))}
                value={groupIDs}
                onChange={(e) =>
                  setGroupIDs(Array.from(e.target.selectedOptions).map((o) => o.value))
                }
                className="w-full rounded-md border border-border bg-panel px-3 py-2 text-sm focus:border-accent focus:outline-none focus:ring-2 focus:ring-accent/30"
              >
                {(groupsQuery.data?.groups ?? []).map((g) => (
                  <option key={g.id} value={g.id}>
                    {g.name} ({g.member_ids.length})
                  </option>
                ))}
              </select>
            </Field>

            {error && <ErrorBox>{error}</ErrorBox>}

            <div className="flex items-center justify-end gap-2 pt-2">
              <Button variant="primary" type="submit" disabled={create.isPending}>
                {create.isPending ? "Generating…" : "Generate install command"}
              </Button>
            </div>
          </form>
        )}
      </PanelBody>
    </Panel>
  );
}

// ---- Shared row helpers ---------------------------------------------------

function TagList({ tags }: { tags: string[] }) {
  if (tags.length === 0) return <span className="text-fg-subtle">—</span>;
  return (
    <div className="flex flex-wrap items-center gap-1">
      {tags.map((t) => (
        <span
          key={t}
          className="inline-flex items-center gap-1 rounded-md bg-panel-2 px-1.5 py-0.5 font-mono text-[10px] text-accent"
        >
          <Tag className="h-3 w-3" aria-hidden />
          {t}
        </span>
      ))}
    </div>
  );
}

function GroupCount({
  ids,
  nameById,
}: {
  ids: string[];
  nameById: Map<string, string>;
}) {
  const count = ids?.length ?? 0;
  if (count === 0) return <span className="text-fg-subtle">—</span>;
  const title = ids.map((id) => nameById.get(id) ?? id).join(", ");
  return (
    <Link to="/admin/groups" title={title} className="text-info hover:underline">
      {count}
    </Link>
  );
}

function LifecyclePill({ state }: { state: Lifecycle }) {
  if (state === "consumed") return <StatusPill status="ok">consumed</StatusPill>;
  if (state === "expired") return <StatusPill status="offline">expired</StatusPill>;
  return <StatusPill status="warn">pending</StatusPill>;
}
