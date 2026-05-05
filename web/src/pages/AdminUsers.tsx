import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { Search } from "lucide-react";
import { FormEvent, useMemo, useState } from "react";

import { Page } from "../components/page";
import {
  Button,
  ErrorBox,
  Field,
  Panel,
  PanelBody,
  PanelHeader,
  Skeleton,
  StatusPill,
  SuccessBox,
  Table,
  TBody,
  TD,
  TH,
  THead,
  TextInput,
} from "../components/ui";
import { api, ApiError } from "../lib/api";
import { AdminCreateUserResponse, AdminUser } from "../lib/types";

// TODO(theme): this page still uses raw `zinc-*` Tailwind classes which
// don't follow the dark/light palette. Migrate to semantic tokens
// (text-fg-muted, bg-panel, border-border, …) in a follow-up.

type ListResponse = { users: AdminUser[] };

type RoleFilter = "all" | "admin" | "user";
type StatusFilter = "all" | "active" | "disabled";

export function AdminUsers() {
  const qc = useQueryClient();
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
            <UserTable users={list.data?.users ?? []} onChange={invalidate} />
          )}
        </PanelBody>
      </Panel>
    </Page>
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

function UserTable({ users, onChange }: { users: AdminUser[]; onChange: () => void }) {
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
            <TH>Actions</TH>
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
                onChange={onChange}
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

function UserRow({
  user,
  onChange,
  checked,
  onCheck,
}: {
  user: AdminUser;
  onChange: () => void;
  checked: boolean;
  onCheck: () => void;
}) {
  const [resetURL, setResetURL] = useState<string | null>(null);
  const post = useMutation({
    mutationFn: (path: string) => api<unknown>(path, { method: "POST" }),
    onSuccess: onChange,
  });
  const reset = useMutation({
    mutationFn: () =>
      api<{ reset_url?: string; invite_sent: boolean }>(
        `/v1/admin/users/${user.id}/reset-password`,
        { method: "POST" },
      ),
    onSuccess: (data) => {
      if (data.reset_url) setResetURL(data.reset_url);
      onChange();
    },
  });
  const del = useMutation({
    mutationFn: () => api<unknown>(`/v1/admin/users/${user.id}`, { method: "DELETE" }),
    onSuccess: onChange,
  });

  const disabled = !!user.disabled_at;

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
          {disabled ? (
            <StatusPill status="fail">locked</StatusPill>
          ) : (
            <StatusPill status="ok">active</StatusPill>
          )}
        </TD>
        <TD className="text-fg-muted">{user.totp_active ? "yes" : "—"}</TD>
        <TD className="text-fg-muted">
          {user.last_login_at ? new Date(user.last_login_at).toLocaleString() : "never"}
        </TD>
        <TD className="space-x-1 text-xs">
          {disabled ? (
            <Button onClick={() => post.mutate(`/v1/admin/users/${user.id}/unlock`)}>
              unlock
            </Button>
          ) : (
            <Button onClick={() => post.mutate(`/v1/admin/users/${user.id}/lock`)}>
              lock
            </Button>
          )}
          <Button onClick={() => reset.mutate()}>reset pw</Button>
          <Button
            onClick={() => post.mutate(`/v1/admin/users/${user.id}/reset-2fa`)}
            disabled={!user.totp_active}
          >
            reset 2fa
          </Button>
          <Button
            variant="danger"
            onClick={() => {
              if (confirm(`Delete ${user.email}?`)) del.mutate();
            }}
          >
            delete
          </Button>
        </TD>
      </tr>
      {resetURL && (
        <tr className="bg-panel-2">
          <td colSpan={7} className="px-3 py-2 font-mono text-xs">
            <span className="text-fg-muted">Reset link:</span>{" "}
            <code className="break-all text-fg">{location.origin + resetURL}</code>
          </td>
        </tr>
      )}
    </>
  );
}
