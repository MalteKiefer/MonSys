import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { FormEvent, useState } from "react";

import {
  Button,
  ErrorBox,
  Field,
  Panel,
  PanelBody,
  PanelHeader,
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

export function AdminUsers() {
  const qc = useQueryClient();
  const list = useQuery({
    queryKey: ["admin-users"],
    queryFn: () => api<ListResponse>("/v1/admin/users"),
  });

  const invalidate = () => qc.invalidateQueries({ queryKey: ["admin-users"] });

  return (
    <div className="mx-auto max-w-5xl space-y-6 p-6">
      <header>
        <h2 className="text-lg font-semibold">Users</h2>
        <p className="text-sm text-fg-muted">Create, lock, reset password, reset 2FA.</p>
      </header>

      <CreateUserCard onCreated={invalidate} />

      <Panel>
        <PanelHeader>
          <h3 className="text-sm font-semibold text-fg">All users</h3>
        </PanelHeader>
        <PanelBody>
          {list.isLoading ? (
            <p className="text-sm text-fg-muted">Loading…</p>
          ) : list.error ? (
            <ErrorBox>{(list.error as Error).message}</ErrorBox>
          ) : (
            <UserTable users={list.data?.users ?? []} onChange={invalidate} />
          )}
        </PanelBody>
      </Panel>
    </div>
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
  return (
    <Table>
      <THead>
        <tr>
          <TH>Email</TH>
          <TH>Role</TH>
          <TH>Status</TH>
          <TH>2FA</TH>
          <TH>Last login</TH>
          <TH>Actions</TH>
        </tr>
      </THead>
      <TBody>
        {users.map((u) => (
          <UserRow key={u.id} user={u} onChange={onChange} />
        ))}
      </TBody>
    </Table>
  );
}

function UserRow({ user, onChange }: { user: AdminUser; onChange: () => void }) {
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
          <td colSpan={6} className="px-3 py-2 font-mono text-xs">
            <span className="text-fg-muted">Reset link:</span>{" "}
            <code className="break-all text-fg">{location.origin + resetURL}</code>
          </td>
        </tr>
      )}
    </>
  );
}
