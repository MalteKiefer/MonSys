import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { FormEvent, useState } from "react";

import { api, ApiError } from "../lib/api";
import { AdminCreateUserResponse, AdminUser } from "../lib/types";
import { Card, Input } from "./Profile";

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
        <p className="text-sm text-zinc-400">Create, lock, reset password, reset 2FA.</p>
      </header>

      <CreateUserCard onCreated={invalidate} />

      <Card title="All users">
        {list.isLoading ? (
          <p className="text-sm text-zinc-400">Loading…</p>
        ) : list.error ? (
          <p className="text-sm text-fail">{(list.error as Error).message}</p>
        ) : (
          <UserTable users={list.data?.users ?? []} onChange={invalidate} />
        )}
      </Card>
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
    <Card title="Create user">
      <form onSubmit={submit} className="space-y-3">
        <Input label="Email" type="email" value={email} onChange={setEmail} />
        <div className="grid gap-3 md:grid-cols-2">
          <label className="block">
            <span className="text-xs text-zinc-400">Role</span>
            <select
              value={role}
              onChange={(e) => setRole(e.target.value as "admin" | "user")}
              className="mt-1 w-full rounded border border-zinc-700 bg-zinc-950 px-3 py-1.5 text-sm focus:border-zinc-500 focus:outline-none"
            >
              <option value="user">user</option>
              <option value="admin">admin</option>
            </select>
          </label>
          <Input
            label="Password (leave empty to invite)"
            type="text"
            required={false}
            value={password}
            onChange={setPassword}
          />
        </div>
        <label className="flex items-center gap-2 text-sm text-zinc-400">
          <input
            type="checkbox"
            checked={sendInvite}
            onChange={(e) => setSendInvite(e.target.checked)}
          />
          Send invite email (requires SMTP configured under Admin → Mail)
        </label>
        <button
          type="submit"
          disabled={busy}
          className="rounded bg-zinc-100 px-3 py-1.5 text-sm font-medium text-zinc-900 hover:bg-white disabled:opacity-50"
        >
          {busy ? "Creating…" : "Create user"}
        </button>
        {msg && (
          <p
            className={`rounded px-3 py-2 text-sm ${
              msg.kind === "ok"
                ? "border border-ok/40 bg-ok/10 text-ok"
                : "border border-fail/40 bg-fail/10 text-fail"
            }`}
          >
            {msg.text}
          </p>
        )}
        {resetURL && (
          <div className="rounded border border-zinc-700 bg-zinc-950 p-3 font-mono text-xs">
            <p className="mb-1 text-zinc-400">Invite link (one-time, expires in 7 days):</p>
            <code className="break-all text-zinc-200">{location.origin + resetURL}</code>
          </div>
        )}
      </form>
    </Card>
  );
}

function UserTable({ users, onChange }: { users: AdminUser[]; onChange: () => void }) {
  return (
    <table className="w-full text-sm">
      <thead className="text-left text-xs uppercase tracking-wider text-zinc-400">
        <tr>
          <th className="px-3 py-2">Email</th>
          <th className="px-3 py-2">Role</th>
          <th className="px-3 py-2">Status</th>
          <th className="px-3 py-2">2FA</th>
          <th className="px-3 py-2">Last login</th>
          <th className="px-3 py-2">Actions</th>
        </tr>
      </thead>
      <tbody className="divide-y divide-zinc-800">
        {users.map((u) => (
          <UserRow key={u.id} user={u} onChange={onChange} />
        ))}
      </tbody>
    </table>
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
      <tr className="hover:bg-zinc-900/40">
        <td className="px-3 py-2 font-medium">{user.email}</td>
        <td className="px-3 py-2 text-zinc-400">{user.role}</td>
        <td className="px-3 py-2">
          {disabled ? (
            <span className="rounded bg-fail/15 px-2 py-0.5 text-xs text-fail">locked</span>
          ) : (
            <span className="rounded bg-ok/15 px-2 py-0.5 text-xs text-ok">active</span>
          )}
        </td>
        <td className="px-3 py-2 text-zinc-400">{user.totp_active ? "yes" : "—"}</td>
        <td className="px-3 py-2 text-zinc-400">
          {user.last_login_at ? new Date(user.last_login_at).toLocaleString() : "never"}
        </td>
        <td className="px-3 py-2 space-x-1 text-xs">
          {disabled ? (
            <button
              onClick={() => post.mutate(`/v1/admin/users/${user.id}/unlock`)}
              className="rounded border border-zinc-700 px-2 py-1 hover:bg-zinc-800"
            >
              unlock
            </button>
          ) : (
            <button
              onClick={() => post.mutate(`/v1/admin/users/${user.id}/lock`)}
              className="rounded border border-zinc-700 px-2 py-1 hover:bg-zinc-800"
            >
              lock
            </button>
          )}
          <button
            onClick={() => reset.mutate()}
            className="rounded border border-zinc-700 px-2 py-1 hover:bg-zinc-800"
          >
            reset pw
          </button>
          <button
            onClick={() => post.mutate(`/v1/admin/users/${user.id}/reset-2fa`)}
            disabled={!user.totp_active}
            className="rounded border border-zinc-700 px-2 py-1 hover:bg-zinc-800 disabled:opacity-40"
          >
            reset 2fa
          </button>
          <button
            onClick={() => {
              if (confirm(`Delete ${user.email}?`)) del.mutate();
            }}
            className="rounded border border-fail/40 px-2 py-1 text-fail hover:bg-fail/10"
          >
            delete
          </button>
        </td>
      </tr>
      {resetURL && (
        <tr className="bg-zinc-950">
          <td colSpan={6} className="px-3 py-2 font-mono text-xs">
            <span className="text-zinc-400">Reset link:</span>{" "}
            <code className="break-all text-zinc-200">{location.origin + resetURL}</code>
          </td>
        </tr>
      )}
    </>
  );
}
