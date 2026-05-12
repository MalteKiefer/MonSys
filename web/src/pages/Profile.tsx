import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { KeyRound, Pencil, Smartphone, Trash2, User } from "lucide-react";
import { ChangeEvent, FormEvent, ReactNode, useState } from "react";
import { useSearchParams } from "react-router-dom";

import {
  Button,
  ErrorBox,
  Field,
  Panel,
  PanelBody,
  PanelHeader,
  Skeleton,
  SuccessBox,
  TabItem,
  Tabs,
  TextInput,
} from "../components/ui";
import { api, ApiError } from "../lib/api";
import { DensityProvider, useDensityStore, type Density } from "../lib/density-store";
import { CurrentUser, ListPasskeysResponse, Passkey, TOTPSetup } from "../lib/types";
import { registerPasskey, supported as webauthnSupported } from "../lib/webauthn";

type Msg = { kind: "ok" | "err"; text: string } | null;

type ProfileTab = "account" | "two_factor" | "passkeys";

const TAB_KEYS: ReadonlyArray<ProfileTab> = ["account", "two_factor", "passkeys"];

function parseTab(raw: string | null): ProfileTab {
  return (TAB_KEYS as readonly string[]).includes(raw ?? "") ? (raw as ProfileTab) : "account";
}

export function Profile() {
  const qc = useQueryClient();
  const [searchParams, setSearchParams] = useSearchParams();
  const tab = parseTab(searchParams.get("tab"));
  const setTab = (next: ProfileTab) => {
    const sp = new URLSearchParams(searchParams);
    sp.set("tab", next);
    setSearchParams(sp, { replace: true });
  };

  const me = useQuery({
    queryKey: ["me"],
    queryFn: () => api<CurrentUser>("/v1/auth/me"),
  });

  if (me.isLoading)
    return (
      <div className="mx-auto max-w-3xl space-y-4 p-6">
        <Skeleton className="h-8 w-48" />
        <Skeleton className="h-32" />
        <Skeleton className="h-32" />
        <Skeleton className="h-48" />
      </div>
    );
  if (me.error) return <p className="p-6 text-sm text-fail">{(me.error as Error).message}</p>;
  const user = me.data!;

  const items: ReadonlyArray<TabItem<ProfileTab>> = [
    { key: "account", label: "Account", icon: User },
    { key: "two_factor", label: "Two-factor", icon: Smartphone },
    { key: "passkeys", label: "Passkeys", icon: KeyRound },
  ];

  return (
    <div className="mx-auto max-w-3xl p-6">
      {/* Mount the html[data-density] side effect from this page. The
          provider is a no-op render — it just mirrors the persisted store
          value onto <html>. Remove once App.tsx (Phase A) hosts it. */}
      <DensityProvider />
      <header className="mb-4">
        <p className="text-sm text-fg-muted">
          Signed in as <span className="text-fg">{user.email}</span> ({user.role})
        </p>
      </header>

      <Tabs<ProfileTab> items={items} value={tab} onChange={setTab} />

      <div
        id={`panel-${tab}`}
        role="tabpanel"
        aria-labelledby={`tab-${tab}`}
        className="mt-6 space-y-6"
      >
        {tab === "account" && (
          <>
            <ChangeEmailCard onSuccess={() => qc.invalidateQueries({ queryKey: ["me"] })} />
            <ChangePasswordCard />
            <DisplayCard />
          </>
        )}
        {tab === "two_factor" && (
          <TwoFactorCard
            active={user.totp_active}
            onSuccess={() => qc.invalidateQueries({ queryKey: ["me"] })}
          />
        )}
        {tab === "passkeys" && <PasskeysCard />}
      </div>
    </div>
  );
}

function DisplayCard() {
  const density = useDensityStore((s) => s.density);
  const setDensity = useDensityStore((s) => s.setDensity);
  const options: { value: Density; label: string; hint: string }[] = [
    { value: "compact", label: "Compact", hint: "Denser tables and tighter panels." },
    { value: "comfortable", label: "Comfortable", hint: "Default spacing." },
  ];
  return (
    <ProfilePanel title="Display">
      <div className="space-y-3">
        <p className="text-sm text-fg-muted">
          Density adjusts table row, panel, and font sizing across the app. The setting is saved
          to this browser.
        </p>
        <div role="radiogroup" aria-label="UI density" className="inline-flex rounded-md border border-border bg-panel p-0.5">
          {options.map((opt) => {
            const active = opt.value === density;
            return (
              <button
                key={opt.value}
                type="button"
                role="radio"
                aria-checked={active}
                onClick={() => setDensity(opt.value)}
                className={`min-h-9 rounded px-3 py-1.5 text-sm font-medium transition-colors duration-150 ${
                  active ? "bg-panel-2 text-fg shadow-panel" : "text-fg-muted hover:text-fg"
                }`}
              >
                {opt.label}
              </button>
            );
          })}
        </div>
        <p className="text-xs text-fg-subtle">
          {options.find((o) => o.value === density)?.hint}
        </p>
      </div>
    </ProfilePanel>
  );
}

function ProfilePanel({ title, children }: { title: string; children: ReactNode }) {
  return (
    <Panel>
      <PanelHeader>
        <h3 className="text-sm font-semibold text-fg">{title}</h3>
      </PanelHeader>
      <PanelBody>{children}</PanelBody>
    </Panel>
  );
}

function ChangeEmailCard({ onSuccess }: { onSuccess: () => void }) {
  const [pw, setPw] = useState("");
  const [email, setEmail] = useState("");
  const [msg, setMsg] = useState<Msg>(null);
  const [busy, setBusy] = useState(false);

  async function submit(e: FormEvent) {
    e.preventDefault();
    setMsg(null);
    setBusy(true);
    try {
      await api<{ ok: boolean }>("/v1/auth/change-email", {
        method: "POST",
        body: JSON.stringify({ current_password: pw, new_email: email }),
      });
      setMsg({ kind: "ok", text: "Email updated." });
      setPw("");
      setEmail("");
      onSuccess();
    } catch (err) {
      setMsg({ kind: "err", text: err instanceof ApiError ? err.detail : "failed" });
    } finally {
      setBusy(false);
    }
  }

  return (
    <ProfilePanel title="Change email">
      <form onSubmit={submit} className="space-y-3">
        <Field label="Current password">
          <TextInput
            type="password"
            required
            value={pw}
            onChange={(e: ChangeEvent<HTMLInputElement>) => setPw(e.target.value)}
          />
        </Field>
        <Field label="New email">
          <TextInput
            type="email"
            required
            value={email}
            onChange={(e) => setEmail(e.target.value)}
          />
        </Field>
        <FormFooter busy={busy} idle="Update email" busyLabel="Updating…" msg={msg} />
      </form>
    </ProfilePanel>
  );
}

function ChangePasswordCard() {
  const [cur, setCur] = useState("");
  const [next, setNext] = useState("");
  const [msg, setMsg] = useState<Msg>(null);
  const [busy, setBusy] = useState(false);

  async function submit(e: FormEvent) {
    e.preventDefault();
    setMsg(null);
    setBusy(true);
    try {
      await api<{ ok: boolean }>("/v1/auth/change-password", {
        method: "POST",
        body: JSON.stringify({ current_password: cur, new_password: next }),
      });
      setMsg({ kind: "ok", text: "Password updated." });
      setCur("");
      setNext("");
    } catch (err) {
      setMsg({ kind: "err", text: err instanceof ApiError ? err.detail : "failed" });
    } finally {
      setBusy(false);
    }
  }

  return (
    <ProfilePanel title="Change password">
      <form onSubmit={submit} className="space-y-3">
        <Field label="Current password">
          <TextInput type="password" required value={cur} onChange={(e) => setCur(e.target.value)} />
        </Field>
        <Field label="New password">
          <TextInput type="password" required value={next} onChange={(e) => setNext(e.target.value)} />
        </Field>
        <FormFooter busy={busy} idle="Update password" busyLabel="Updating…" msg={msg} />
      </form>
    </ProfilePanel>
  );
}

function TwoFactorCard({ active, onSuccess }: { active: boolean; onSuccess: () => void }) {
  const [setup, setSetup] = useState<TOTPSetup | null>(null);
  const [code, setCode] = useState("");
  const [pw, setPw] = useState("");
  const [msg, setMsg] = useState<Msg>(null);
  const [busy, setBusy] = useState(false);

  async function startSetup() {
    setMsg(null);
    setBusy(true);
    try {
      const s = await api<TOTPSetup>("/v1/auth/2fa/setup", { method: "POST" });
      setSetup(s);
    } catch (err) {
      setMsg({ kind: "err", text: err instanceof ApiError ? err.detail : "failed" });
    } finally {
      setBusy(false);
    }
  }

  async function verify(e: FormEvent) {
    e.preventDefault();
    setMsg(null);
    setBusy(true);
    try {
      await api<{ ok: boolean }>("/v1/auth/2fa/verify", {
        method: "POST",
        body: JSON.stringify({ code }),
      });
      setMsg({ kind: "ok", text: "Two-factor authentication enabled. Save your backup codes!" });
      setCode("");
      onSuccess();
    } catch (err) {
      setMsg({ kind: "err", text: err instanceof ApiError ? err.detail : "failed" });
    } finally {
      setBusy(false);
    }
  }

  async function disable(e: FormEvent) {
    e.preventDefault();
    setMsg(null);
    setBusy(true);
    try {
      await api<{ ok: boolean }>("/v1/auth/2fa/disable", {
        method: "POST",
        body: JSON.stringify({ password: pw }),
      });
      setMsg({ kind: "ok", text: "Two-factor authentication disabled." });
      setPw("");
      setSetup(null);
      onSuccess();
    } catch (err) {
      setMsg({ kind: "err", text: err instanceof ApiError ? err.detail : "failed" });
    } finally {
      setBusy(false);
    }
  }

  return (
    <ProfilePanel title={`Two-factor authentication (${active ? "active" : "off"})`}>
      {active ? (
        <form onSubmit={disable} className="space-y-3">
          <p className="text-sm text-fg-muted">TOTP is active. To remove, confirm your password.</p>
          <Field label="Password">
            <TextInput type="password" required value={pw} onChange={(e) => setPw(e.target.value)} />
          </Field>
          <FormFooter busy={busy} idle="Disable 2FA" busyLabel="Disabling…" msg={msg} variant="danger" />
        </form>
      ) : !setup ? (
        <div className="space-y-3">
          <p className="text-sm text-fg-muted">
            Add a second factor by scanning a QR code in your authenticator (Aegis, 1Password, etc.).
          </p>
          <Button variant="primary" onClick={startSetup} disabled={busy}>
            {busy ? "Generating…" : "Begin setup"}
          </Button>
          {msg && <Message msg={msg} />}
        </div>
      ) : (
        <div className="space-y-4">
          <div className="grid gap-4 md:grid-cols-2">
            <div>
              <p className="mb-2 text-xs uppercase tracking-wider text-fg-muted">Scan with authenticator</p>
              <img
                src={`data:image/png;base64,${setup.qr_png_base64}`}
                alt="TOTP QR code"
                className="rounded border border-border bg-white p-2"
              />
              <p className="mt-2 text-xs text-fg-subtle">
                Or enter the secret manually:
                <code className="ml-1 select-all rounded bg-panel-2 px-1 py-0.5 font-mono text-xs text-fg">
                  {setup.secret_b32}
                </code>
              </p>
            </div>
            <div>
              <p className="mb-2 text-xs uppercase tracking-wider text-fg-muted">Backup codes (save once!)</p>
              <ul className="grid grid-cols-2 gap-1 rounded border border-border bg-bg p-3 font-mono text-xs text-fg">
                {setup.backup_codes.map((c) => (
                  <li key={c} className="select-all">
                    {c}
                  </li>
                ))}
              </ul>
            </div>
          </div>

          <form onSubmit={verify} className="space-y-3">
            <Field label="Confirm with a current 6-digit code">
              <TextInput
                type="text"
                required
                value={code}
                onChange={(e) => setCode(e.target.value)}
                className="font-mono tracking-widest"
              />
            </Field>
            <FormFooter busy={busy} idle="Activate 2FA" busyLabel="Verifying…" msg={msg} />
          </form>
        </div>
      )}
    </ProfilePanel>
  );
}

function PasskeysCard() {
  const qc = useQueryClient();
  const [name, setName] = useState("");
  const [msg, setMsg] = useState<Msg>(null);

  const passkeys = useQuery({
    queryKey: ["passkeys"],
    queryFn: () => api<ListPasskeysResponse>("/v1/auth/me/passkeys"),
    enabled: webauthnSupported(),
  });

  const addPasskey = useMutation({
    mutationFn: async (n: string) => registerPasskey(n),
    onSuccess: () => {
      setName("");
      setMsg({ kind: "ok", text: "Passkey hinzugefügt." });
      qc.invalidateQueries({ queryKey: ["passkeys"] });
      qc.invalidateQueries({ queryKey: ["me"] });
    },
    onError: (err) => {
      setMsg({ kind: "err", text: err instanceof ApiError ? err.detail : (err as Error).message });
    },
  });

  const renamePasskey = useMutation({
    mutationFn: ({ id, name: n }: { id: string; name: string }) =>
      api<{ ok: boolean }>(`/v1/auth/me/passkeys/${id}`, {
        method: "PUT",
        body: JSON.stringify({ name: n }),
      }),
    onSuccess: () => {
      setMsg(null);
      qc.invalidateQueries({ queryKey: ["passkeys"] });
    },
    onError: (err) => {
      setMsg({ kind: "err", text: err instanceof ApiError ? err.detail : (err as Error).message });
    },
  });

  const deletePasskey = useMutation({
    mutationFn: (id: string) =>
      api<{ ok: boolean }>(`/v1/auth/me/passkeys/${id}`, { method: "DELETE" }),
    onSuccess: () => {
      setMsg(null);
      qc.invalidateQueries({ queryKey: ["passkeys"] });
      qc.invalidateQueries({ queryKey: ["me"] });
    },
    onError: (err) => {
      setMsg({ kind: "err", text: err instanceof ApiError ? err.detail : (err as Error).message });
    },
  });

  async function submitAdd(e: FormEvent) {
    e.preventDefault();
    setMsg(null);
    const trimmed = name.trim();
    if (!trimmed) {
      setMsg({ kind: "err", text: "Bitte einen Namen vergeben." });
      return;
    }
    addPasskey.mutate(trimmed);
  }

  if (!webauthnSupported()) {
    return (
      <ProfilePanel title="Passkeys">
        <div className="space-y-2">
          <p className="text-sm text-fg-muted">
            Sicherer und schneller Login ohne Passwort. Browser oder Hardware-Key.
          </p>
          <p className="text-sm text-fg-subtle">Dein Browser unterstützt keine Passkeys.</p>
        </div>
      </ProfilePanel>
    );
  }

  const list = passkeys.data?.passkeys ?? [];

  return (
    <ProfilePanel title="Passkeys">
      <div className="space-y-4">
        <p className="text-sm text-fg-muted">
          Sicherer und schneller Login ohne Passwort. Browser oder Hardware-Key.
        </p>

        <form onSubmit={submitAdd} className="flex flex-wrap items-end gap-2">
          <div className="flex-1 min-w-[12rem]">
            <Field label="Passkey-Name">
              <TextInput
                type="text"
                placeholder="z.B. Laptop, YubiKey 5C"
                value={name}
                onChange={(e: ChangeEvent<HTMLInputElement>) => setName(e.target.value)}
                disabled={addPasskey.isPending}
              />
            </Field>
          </div>
          <Button type="submit" variant="primary" disabled={addPasskey.isPending}>
            <KeyRound className="h-3.5 w-3.5" />
            {addPasskey.isPending ? "Registriere…" : "Passkey hinzufügen"}
          </Button>
        </form>

        {msg && <Message msg={msg} />}

        {passkeys.isLoading ? (
          <Skeleton className="h-16" />
        ) : passkeys.error ? (
          <ErrorBox>{(passkeys.error as Error).message}</ErrorBox>
        ) : list.length === 0 ? (
          <p className="text-sm text-fg-subtle">Noch keine Passkeys registriert.</p>
        ) : (
          <ul className="divide-y divide-border rounded border border-border bg-bg">
            {list.map((pk) => (
              <PasskeyRow
                key={pk.id}
                passkey={pk}
                onRename={(newName) => renamePasskey.mutate({ id: pk.id, name: newName })}
                onDelete={() => deletePasskey.mutate(pk.id)}
                renaming={renamePasskey.isPending}
                deleting={deletePasskey.isPending}
              />
            ))}
          </ul>
        )}
      </div>
    </ProfilePanel>
  );
}

function PasskeyRow({
  passkey,
  onRename,
  onDelete,
  renaming,
  deleting,
}: {
  passkey: Passkey;
  onRename: (name: string) => void;
  onDelete: () => void;
  renaming: boolean;
  deleting: boolean;
}) {
  const [editing, setEditing] = useState(false);
  const [draft, setDraft] = useState(passkey.name);

  function saveName(e: FormEvent) {
    e.preventDefault();
    const trimmed = draft.trim();
    if (!trimmed || trimmed === passkey.name) {
      setEditing(false);
      setDraft(passkey.name);
      return;
    }
    onRename(trimmed);
    setEditing(false);
  }

  function confirmDelete() {
    if (window.confirm(`Passkey "${passkey.name}" wirklich löschen?`)) {
      onDelete();
    }
  }

  const lastUsed = passkey.last_used_at
    ? new Date(passkey.last_used_at).toLocaleString()
    : "—";

  return (
    <li className="flex flex-wrap items-center gap-3 px-3 py-2 text-sm">
      <div className="flex flex-1 min-w-[12rem] items-center gap-2">
        <KeyRound className="h-4 w-4 text-fg-subtle" aria-hidden />
        {editing ? (
          <form onSubmit={saveName} className="flex flex-1 items-center gap-2">
            <TextInput
              type="text"
              autoFocus
              value={draft}
              onChange={(e: ChangeEvent<HTMLInputElement>) => setDraft(e.target.value)}
              disabled={renaming}
              className="flex-1"
            />
            <Button type="submit" variant="primary" disabled={renaming}>
              {renaming ? "…" : "Save"}
            </Button>
            <Button
              type="button"
              variant="ghost"
              onClick={() => {
                setEditing(false);
                setDraft(passkey.name);
              }}
              disabled={renaming}
            >
              Cancel
            </Button>
          </form>
        ) : (
          <>
            <span className="font-medium text-fg">{passkey.name}</span>
            {passkey.aaguid && (
              <span className="font-mono text-xs text-fg-subtle">{passkey.aaguid.slice(0, 8)}</span>
            )}
          </>
        )}
      </div>
      {!editing && (
        <>
          <span className="text-xs text-fg-muted">Last used: {lastUsed}</span>
          <div className="flex items-center gap-1">
            <Button
              type="button"
              variant="ghost"
              onClick={() => setEditing(true)}
              disabled={renaming || deleting}
              aria-label="Rename passkey"
            >
              <Pencil className="h-3.5 w-3.5" />
            </Button>
            <Button
              type="button"
              variant="danger"
              onClick={confirmDelete}
              disabled={renaming || deleting}
              aria-label="Delete passkey"
            >
              <Trash2 className="h-3.5 w-3.5" />
            </Button>
          </div>
        </>
      )}
    </li>
  );
}

function Message({ msg }: { msg: { kind: "ok" | "err"; text: string } }) {
  return msg.kind === "ok" ? <SuccessBox>{msg.text}</SuccessBox> : <ErrorBox>{msg.text}</ErrorBox>;
}

function FormFooter({
  busy,
  idle,
  busyLabel,
  msg,
  variant,
}: {
  busy: boolean;
  idle: string;
  busyLabel: string;
  msg: Msg;
  variant?: "danger";
}) {
  return (
    <div className="space-y-2">
      <Button type="submit" variant={variant === "danger" ? "danger" : "primary"} disabled={busy}>
        {busy ? busyLabel : idle}
      </Button>
      {msg && <Message msg={msg} />}
    </div>
  );
}
