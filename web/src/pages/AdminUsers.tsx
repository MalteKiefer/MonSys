import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import {
  Lock,
  LockOpen,
  LogOut,
  Mail,
  MoreVertical,
  Search,
  Send,
  ShieldOff,
  Trash2,
  Users,
} from "lucide-react";
import { FormEvent, useEffect, useMemo, useState } from "react";

import { Page } from "../components/page";
import {
  Button,
  DropdownMenu,
  DropdownItem,
  Empty,
  ErrorBox,
  Field,
  Panel,
  PanelBody,
  PanelHeader,
  Skeleton,
  StatusPill,
  SuccessBox,
  Table,
  TabItem,
  Tabs,
  TBody,
  TD,
  TH,
  THead,
  TextInput,
} from "../components/ui";
import { api, ApiError } from "../lib/api";
import { useAuth } from "../lib/auth";
import { AdminCreateUserResponse, AdminUser } from "../lib/types";

// TODO(theme): this page still uses raw `zinc-*` Tailwind classes which
// don't follow the dark/light palette. Migrate to semantic tokens
// (text-fg-muted, bg-panel, border-border, …) in a follow-up.

type ListResponse = { users: AdminUser[] };

type RoleFilter = "all" | "admin" | "user";
type StatusFilter = "all" | "active" | "disabled";

// Tab keys are kept as a string union so the Tabs primitive's generic gives
// us a typed `onChange`. Audit is intentionally absent — this page has no
// user-audit logic to surface.
type TabKey = "list" | "invites";

const TABS: ReadonlyArray<TabItem<TabKey>> = [
  { key: "list", label: "Users", icon: Users },
  { key: "invites", label: "Invites", icon: Send },
];

// ---- Page-level toast banner ---------------------------------------------

// No global toast primitive exists in the design system yet, so this page
// renders an inline auto-dismissing banner pinned to the top of the panel
// content. The banner reuses Success/ErrorBox styling for visual parity.
type Toast = { kind: "ok" | "err"; text: string };

function ToastBanner({
  toast,
  onClose,
}: {
  toast: Toast | null;
  onClose: () => void;
}) {
  useEffect(() => {
    if (!toast) return;
    const t = window.setTimeout(onClose, 3500);
    return () => window.clearTimeout(t);
  }, [toast, onClose]);
  if (!toast) return null;
  return (
    <div role="status" aria-live="polite">
      {toast.kind === "ok" ? (
        <SuccessBox>{toast.text}</SuccessBox>
      ) : (
        <ErrorBox>{toast.text}</ErrorBox>
      )}
    </div>
  );
}

export function AdminUsers() {
  const qc = useQueryClient();
  const [tab, setTab] = useState<TabKey>("list");
  const [toast, setToast] = useState<Toast | null>(null);

  const list = useQuery({
    queryKey: ["admin-users"],
    queryFn: () => api<ListResponse>("/v1/admin/users"),
  });

  const invalidate = () => qc.invalidateQueries({ queryKey: ["admin-users"] });

  return (
    <Page
      title="Users"
      subtitle="Create, lock, reset password, reset 2FA."
      breadcrumb={[{ label: "Admin" }, { label: "Users" }]}
    >
      <Tabs items={TABS} value={tab} onChange={setTab} />

      {tab === "list" && (
        <div
          role="tabpanel"
          id="panel-list"
          aria-labelledby="tab-list"
          className="space-y-6"
        >
          <ToastBanner toast={toast} onClose={() => setToast(null)} />

          <CreateUserCard onCreated={invalidate} />

          <Panel>
            <PanelHeader>
              <h3 className="text-sm font-semibold text-fg">All users</h3>
            </PanelHeader>
            <PanelBody>
              {list.isLoading ? (
                <Skeleton className="h-48" />
              ) : list.error ? (
                <ErrorBox>{(list.error as Error).message}</ErrorBox>
              ) : (
                <UserTable
                  users={list.data?.users ?? []}
                  onChange={invalidate}
                  onToast={setToast}
                />
              )}
            </PanelBody>
          </Panel>
        </div>
      )}

      {tab === "invites" && (
        <div
          role="tabpanel"
          id="panel-invites"
          aria-labelledby="tab-invites"
        >
          <InvitesPlaceholder />
        </div>
      )}
    </Page>
  );
}

// Placeholder for the future Invites tab. The backend already issues
// invite/reset tokens via `user_action_tokens`, but exposes no list endpoint
// — once `GET /v1/admin/invites` (and a "Create invite" mutation) land,
// replace this with a real table + create-invite form.
function InvitesPlaceholder() {
  return (
    <Panel>
      <PanelHeader>
        <h3 className="text-sm font-semibold text-fg">Pending invites</h3>
      </PanelHeader>
      <PanelBody>
        <Empty>
          Invite-token listing is not yet exposed by the API. Invites are
          currently issued inline when creating a user (Users tab) — the
          one-time link is shown once and can be copied from the success
          banner. A dedicated endpoint to list, re-send, and revoke pending
          invites is planned.
        </Empty>
      </PanelBody>
    </Panel>
  );
}

function CreateUserCard({ onCreated }: { onCreated: () => void }) {
  const [email, setEmail] = useState("");
  const [role, setRole] = useState<"admin" | "user">("user");
  const [password, setPassword] = useState("");
  const [sendInvite, setSendInvite] = useState(false);
  const [busy, setBusy] = useState(false);
  const [resetURL, setResetURL] = useState<string | null>(null);
  const [msg, setMsg] = useState<{ kind: "ok" | "err"; text: string } | null>(null);

  async function submit(e: FormEvent) {
    e.preventDefault();
    setBusy(true);
    setMsg(null);
    setResetURL(null);
    try {
      const resp = await api<AdminCreateUserResponse>("/v1/admin/users", {
        method: "POST",
        body: JSON.stringify({
          email,
          role,
          password: password || undefined,
          send_invite: sendInvite,
        }),
      });
      setEmail("");
      setPassword("");
      if (resp.reset_url) setResetURL(resp.reset_url);
      setMsg({
        kind: "ok",
        text: resp.invite_sent
          ? `Invite mailed to ${resp.user.email}.`
          : password
          ? `User ${resp.user.email} created.`
          : `User ${resp.user.email} created. Copy the invite link below — it is shown only once.`,
      });
      onCreated();
    } catch (err) {
      setMsg({ kind: "err", text: err instanceof ApiError ? err.detail : "failed" });
    } finally {
      setBusy(false);
    }
  }

  return (
    <Panel>
      <PanelHeader>
        <h3 className="text-sm font-semibold text-fg">Create user</h3>
      </PanelHeader>
      <PanelBody>
        <form onSubmit={submit} className="space-y-3">
          <Field label="Email">
            <TextInput
              type="email"
              required
              value={email}
              onChange={(e) => setEmail(e.target.value)}
            />
          </Field>
          <div className="grid gap-3 md:grid-cols-2">
            <Field label="Role">
              <select
                value={role}
                onChange={(e) => setRole(e.target.value as "admin" | "user")}
                className="w-full rounded-md border border-border bg-panel px-3 py-2 text-sm text-fg transition-colors duration-150 focus:border-accent focus:outline-none focus:ring-2 focus:ring-accent/30"
              >
                <option value="user">user</option>
                <option value="admin">admin</option>
              </select>
            </Field>
            <Field label="Password (leave empty to invite)">
              <TextInput
                type="text"
                value={password}
                onChange={(e) => setPassword(e.target.value)}
              />
            </Field>
          </div>
          <label className="flex items-center gap-2 text-sm text-fg-muted">
            <input
              type="checkbox"
              checked={sendInvite}
              onChange={(e) => setSendInvite(e.target.checked)}
            />
            Send invite email (requires SMTP configured under Admin → Mail)
          </label>
          <Button type="submit" variant="primary" disabled={busy}>
            {busy ? "Creating…" : "Create user"}
          </Button>
          {msg &&
            (msg.kind === "ok" ? (
              <SuccessBox>{msg.text}</SuccessBox>
            ) : (
              <ErrorBox>{msg.text}</ErrorBox>
            ))}
          {resetURL && (
            <div className="rounded-md border border-border bg-panel-2 p-3 font-mono text-xs">
              <p className="mb-1 text-fg-muted">Invite link (one-time, expires in 7 days):</p>
              <code className="break-all text-fg">{location.origin + resetURL}</code>
            </div>
          )}
        </form>
      </PanelBody>
    </Panel>
  );
}

function UserTable({
  users,
  onChange,
  onToast,
}: {
  users: AdminUser[];
  onChange: () => void;
  onToast: (t: Toast) => void;
}) {
  const me = useAuth((s) => s.user);
  const [search, setSearch] = useState("");
  const [roleFilter, setRoleFilter] = useState<RoleFilter>("all");
  const [statusFilter, setStatusFilter] = useState<StatusFilter>("all");
  const [selected, setSelected] = useState<Set<string>>(new Set());
  const [bulkBusy, setBulkBusy] = useState(false);
  const [bulkErr, setBulkErr] = useState<string | null>(null);

  // Filter users client-side; the network surface is unchanged. Email
  // search is a substring match (case-insensitive).
  const visible = useMemo(() => {
    const s = search.trim().toLowerCase();
    return users.filter((u) => {
      if (s && !u.email.toLowerCase().includes(s)) return false;
      if (roleFilter !== "all" && u.role !== roleFilter) return false;
      const isDisabled = !!u.disabled_at;
      if (statusFilter === "active" && isDisabled) return false;
      if (statusFilter === "disabled" && !isDisabled) return false;
      return true;
    });
  }, [users, search, roleFilter, statusFilter]);

  // Restrict the "selected" view to currently-visible rows so the bulk
  // toolbar never operates on rows the user has filtered out.
  const visibleIDs = useMemo(() => new Set(visible.map((u) => u.id)), [visible]);
  const selectedVisible = useMemo(
    () => visible.filter((u) => selected.has(u.id)),
    [visible, selected],
  );

  // Count of currently-enabled admins drives the "last admin" UX gates.
  // The server also enforces these invariants but the client-side gate keeps
  // unsafe options out of the menu entirely.
  const enabledAdminCount = useMemo(
    () => users.filter((u) => u.role === "admin" && !u.disabled_at).length,
    [users],
  );

  const allChecked = visible.length > 0 && selectedVisible.length === visible.length;
  const someChecked = selectedVisible.length > 0 && !allChecked;

  function toggleOne(id: string) {
    setSelected((prev) => {
      const next = new Set(prev);
      if (next.has(id)) next.delete(id);
      else next.add(id);
      return next;
    });
  }

  function toggleAllVisible() {
    setSelected((prev) => {
      const next = new Set(prev);
      if (allChecked) {
        for (const id of visibleIDs) next.delete(id);
      } else {
        for (const id of visibleIDs) next.add(id);
      }
      return next;
    });
  }

  async function runBulk(action: "lock" | "unlock") {
    // Skip rows where the action would be a no-op so we don't fire
    // useless requests against already-locked / already-active users.
    const targets = selectedVisible.filter((u) =>
      action === "lock" ? !u.disabled_at : !!u.disabled_at,
    );
    if (targets.length === 0) {
      setBulkErr(
        `No selected users need to be ${action === "lock" ? "disabled" : "enabled"}.`,
      );
      return;
    }
    const verb = action === "lock" ? "Disable" : "Enable";
    if (
      !window.confirm(
        `${verb} ${targets.length} user${targets.length === 1 ? "" : "s"}?`,
      )
    ) {
      return;
    }
    setBulkBusy(true);
    setBulkErr(null);
    try {
      await Promise.all(
        targets.map((u) =>
          api<unknown>(`/v1/admin/users/${u.id}/${action}`, { method: "POST" }),
        ),
      );
      setSelected(new Set());
      onChange();
    } catch (err) {
      setBulkErr(err instanceof ApiError ? err.detail : (err as Error).message);
    } finally {
      setBulkBusy(false);
    }
  }

  return (
    <div className="space-y-3">
      <div className="flex flex-wrap items-center gap-2">
        <div className="relative min-w-[220px] flex-1">
          <Search className="pointer-events-none absolute left-2.5 top-1/2 h-4 w-4 -translate-y-1/2 text-fg-subtle" />
          <TextInput
            type="search"
            placeholder="search email…"
            value={search}
            onChange={(e) => setSearch(e.target.value)}
            className="pl-8"
          />
        </div>
        <select
          value={roleFilter}
          onChange={(e) => setRoleFilter(e.target.value as RoleFilter)}
          className="rounded-md border border-border bg-panel px-3 py-2 text-sm focus:border-accent focus:outline-none"
          aria-label="Role filter"
        >
          <option value="all">All roles</option>
          <option value="admin">admin</option>
          <option value="user">user</option>
        </select>
        <select
          value={statusFilter}
          onChange={(e) => setStatusFilter(e.target.value as StatusFilter)}
          className="rounded-md border border-border bg-panel px-3 py-2 text-sm focus:border-accent focus:outline-none"
          aria-label="Status filter"
        >
          <option value="all">All statuses</option>
          <option value="active">active</option>
          <option value="disabled">disabled</option>
        </select>
        <span className="text-xs text-fg-subtle tabular-nums">
          {visible.length} of {users.length}
        </span>
      </div>

      {selectedVisible.length > 0 && (
        <div className="flex flex-wrap items-center gap-2 rounded-md border border-accent/30 bg-panel-2 px-3 py-2 text-sm">
          <span className="text-fg">{selectedVisible.length} selected</span>
          <div className="ml-auto flex gap-2">
            <Button size="sm" onClick={() => runBulk("unlock")} disabled={bulkBusy}>
              Enable selected
            </Button>
            <Button
              size="sm"
              variant="danger"
              onClick={() => runBulk("lock")}
              disabled={bulkBusy}
            >
              Disable selected
            </Button>
            <Button
              size="sm"
              variant="ghost"
              onClick={() => setSelected(new Set())}
              disabled={bulkBusy}
            >
              clear
            </Button>
          </div>
        </div>
      )}
      {bulkErr && <ErrorBox>{bulkErr}</ErrorBox>}

      <Table>
        <THead>
          <tr>
            <TH className="w-8">
              <input
                type="checkbox"
                aria-label="Select all visible"
                checked={allChecked}
                ref={(el) => {
                  if (el) el.indeterminate = someChecked;
                }}
                onChange={toggleAllVisible}
                disabled={visible.length === 0}
              />
            </TH>
            <TH>Email</TH>
            <TH>Role</TH>
            <TH>Status</TH>
            <TH>2FA</TH>
            <TH>Last login</TH>
            <TH className="w-12 text-right">Actions</TH>
          </tr>
        </THead>
        <TBody>
          {visible.length === 0 ? (
            <tr>
              <td colSpan={7} className="px-3 py-4 text-sm text-fg-subtle">
                No users match the current filters.
              </td>
            </tr>
          ) : (
            visible.map((u) => (
              <UserRow
                key={u.id}
                user={u}
                meId={me?.id ?? null}
                enabledAdminCount={enabledAdminCount}
                onChange={onChange}
                onToast={onToast}
                checked={selected.has(u.id)}
                onCheck={() => toggleOne(u.id)}
              />
            ))
          )}
        </TBody>
      </Table>
    </div>
  );
}

// ---- Per-row component ---------------------------------------------------

function UserRow({
  user,
  meId,
  enabledAdminCount,
  onChange,
  onToast,
  checked,
  onCheck,
}: {
  user: AdminUser;
  meId: string | null;
  enabledAdminCount: number;
  onChange: () => void;
  onToast: (t: Toast) => void;
  checked: boolean;
  onCheck: () => void;
}) {
  // Modal state for the rare SMTP-failure fallback: the reset URL is shown
  // inline only when the server couldn't email it. Otherwise we never expose
  // the link in the UI.
  const [resetFallbackURL, setResetFallbackURL] = useState<string | null>(null);

  const isDisabled = !!user.disabled_at;
  const isSelf = meId !== null && user.id === meId;
  // "Last admin" — the user is the only enabled admin left. We use this to
  // gate Lock and Delete (which would also kick the last admin out).
  const isLastAdmin =
    user.role === "admin" && enabledAdminCount === 1 && !user.disabled_at;

  const reportError = (err: unknown) => {
    const text = err instanceof ApiError ? err.detail : (err as Error).message;
    onToast({ kind: "err", text });
  };

  // Generic POST mutation reused for lock/unlock/reset-2fa/revoke-sessions.
  const post = useMutation({
    mutationFn: (path: string) => api<unknown>(path, { method: "POST" }),
    onSuccess: onChange,
    onError: reportError,
  });

  const reset = useMutation({
    mutationFn: () =>
      api<{ reset_url?: string; invite_sent: boolean }>(
        `/v1/admin/users/${user.id}/reset-password`,
        { method: "POST" },
      ),
    onSuccess: (data) => {
      if (data.reset_url) {
        // SMTP unavailable — surface the URL so the admin can hand it over
        // out-of-band. Stays in a modal until the admin dismisses it.
        setResetFallbackURL(data.reset_url);
      } else if (data.invite_sent) {
        onToast({
          kind: "ok",
          text: `Password reset link sent to ${user.email}.`,
        });
      } else {
        // Shouldn't happen with the current backend contract, but keep a
        // generic acknowledgement so the user gets feedback either way.
        onToast({ kind: "ok", text: `Reset link issued for ${user.email}.` });
      }
      onChange();
    },
    onError: reportError,
  });

  const del = useMutation({
    mutationFn: () =>
      api<unknown>(`/v1/admin/users/${user.id}`, { method: "DELETE" }),
    onSuccess: () => {
      onToast({ kind: "ok", text: `User ${user.email} deleted.` });
      onChange();
    },
    onError: reportError,
  });

  // ---- Confirm helpers ----------------------------------------------------
  // Native confirm() is good enough for these destructive actions — they're
  // admin-only and infrequent. A custom modal could come later if needed.

  const onResetPassword = () => reset.mutate();

  const onReset2FA = () => {
    if (
      !window.confirm(
        `Wipes the user's TOTP enrollment. They will need to re-enroll on next login. Continue?`,
      )
    ) {
      return;
    }
    post.mutate(`/v1/admin/users/${user.id}/reset-2fa`, {
      onSuccess: () => {
        onToast({ kind: "ok", text: `2FA reset for ${user.email}.` });
        onChange();
      },
    });
  };

  const onRevokeSessions = () => {
    if (
      !window.confirm(
        `Force ${user.email} to re-authenticate immediately on every device?`,
      )
    ) {
      return;
    }
    post.mutate(`/v1/admin/users/${user.id}/revoke-sessions`, {
      onSuccess: () => {
        onToast({
          kind: "ok",
          text: `All sessions revoked for ${user.email}.`,
        });
        onChange();
      },
    });
  };

  const onLockToggle = () => {
    const action = isDisabled ? "unlock" : "lock";
    post.mutate(`/v1/admin/users/${user.id}/${action}`, {
      onSuccess: () => {
        onToast({
          kind: "ok",
          text: `${user.email} ${action === "lock" ? "locked" : "unlocked"}.`,
        });
        onChange();
      },
    });
  };

  const onDelete = () => {
    if (!window.confirm(`Delete ${user.email}? This cannot be undone.`)) return;
    del.mutate();
  };

  // ---- Menu item construction --------------------------------------------
  //
  // Each item carries client-side gating (`disabled` + `disabledReason`). The
  // server still enforces these invariants — this is purely UX so admins
  // don't see an action that's guaranteed to fail. Ordering is roughly
  // "least destructive" → "most destructive".

  const items: DropdownItem[] = [
    {
      key: "reset-password",
      label: "Reset password",
      icon: Mail,
      onClick: onResetPassword,
    },
    {
      key: "reset-2fa",
      label: "Reset 2FA / MFA",
      icon: ShieldOff,
      destructive: true,
      onClick: onReset2FA,
      disabled: !user.totp_active,
      disabledReason: !user.totp_active
        ? "This user has no TOTP enrollment to reset."
        : undefined,
    },
    {
      key: "revoke-sessions",
      label: "End all sessions",
      icon: LogOut,
      destructive: true,
      onClick: onRevokeSessions,
      disabled: isSelf,
      disabledReason: isSelf
        ? "Use the logout button for your own session."
        : undefined,
    },
    {
      key: "lock-unlock",
      label: isDisabled ? "Unlock" : "Lock",
      icon: isDisabled ? LockOpen : Lock,
      onClick: onLockToggle,
      // Locking yourself or the last enabled admin would brick the install.
      disabled: !isDisabled && (isSelf || isLastAdmin),
      disabledReason:
        !isDisabled && isSelf
          ? "You can't lock yourself out."
          : !isDisabled && isLastAdmin
            ? "This is the only enabled admin — promote or unlock another first."
            : undefined,
    },
    {
      key: "delete",
      label: "Delete",
      icon: Trash2,
      destructive: true,
      onClick: onDelete,
      disabled: isSelf || isLastAdmin,
      disabledReason: isSelf
        ? "You can't delete your own account."
        : isLastAdmin
          ? "This is the only enabled admin — can't delete the last admin."
          : undefined,
    },
  ];

  return (
    <>
      <tr className="hover:bg-panel-2/40">
        <TD>
          <input
            type="checkbox"
            aria-label={`Select ${user.email}`}
            checked={checked}
            onChange={onCheck}
          />
        </TD>
        <TD className="font-medium">{user.email}</TD>
        <TD className="text-fg-muted">{user.role}</TD>
        <TD>
          {isDisabled ? (
            <StatusPill status="fail">locked</StatusPill>
          ) : (
            <StatusPill status="ok">active</StatusPill>
          )}
        </TD>
        <TD className="text-fg-muted">{user.totp_active ? "yes" : "—"}</TD>
        <TD className="text-fg-muted">
          {user.last_login_at ? new Date(user.last_login_at).toLocaleString() : "never"}
        </TD>
        <TD className="text-right">
          <DropdownMenu
            align="right"
            trigger={
              <button
                type="button"
                aria-label={`Actions for ${user.email}`}
                className="inline-flex h-8 w-8 items-center justify-center rounded-md text-fg-muted transition-colors duration-150 hover:bg-panel-2 hover:text-fg focus:outline-none focus-visible:ring-2 focus-visible:ring-accent/40"
              >
                <MoreVertical className="h-4 w-4" />
              </button>
            }
            items={items}
          />
        </TD>
      </tr>
      {resetFallbackURL && (
        <tr>
          <td colSpan={7} className="px-0">
            <ResetURLDialog
              email={user.email}
              url={resetFallbackURL}
              onClose={() => setResetFallbackURL(null)}
            />
          </td>
        </tr>
      )}
    </>
  );
}

// ---- Reset URL fallback dialog -------------------------------------------

// Rendered only when the API returns `reset_url` — i.e. SMTP was not
// configured (or send failed). Shows the link with a Copy button and a
// loud warning so the admin knows mail did *not* go out.
function ResetURLDialog({
  email,
  url,
  onClose,
}: {
  email: string;
  url: string;
  onClose: () => void;
}) {
  const [copied, setCopied] = useState(false);
  const fullURL = location.origin + url;

  async function copy() {
    try {
      await navigator.clipboard.writeText(fullURL);
      setCopied(true);
      window.setTimeout(() => setCopied(false), 1500);
    } catch {
      // Clipboard API can fail under HTTP/non-secure contexts — fall back
      // to a manual selection prompt so the admin can still grab the URL.
      window.prompt("Copy the reset link:", fullURL);
    }
  }

  return (
    <div className="my-2 mx-3 space-y-2 rounded-md border border-warn/40 bg-warn/5 p-3">
      <p className="text-sm font-medium text-warn">
        SMTP not configured or send failed — hand this to {email} out-of-band.
      </p>
      <div className="flex items-center gap-2">
        <code className="flex-1 break-all rounded border border-border bg-panel-2 px-2 py-1 font-mono text-xs text-fg">
          {fullURL}
        </code>
        <Button size="sm" onClick={copy}>
          {copied ? "Copied" : "Copy"}
        </Button>
        <Button size="sm" variant="ghost" onClick={onClose}>
          Dismiss
        </Button>
      </div>
    </div>
  );
}
