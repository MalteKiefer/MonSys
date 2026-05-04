import { useQuery, useQueryClient } from "@tanstack/react-query";
import { FormEvent, useState } from "react";

import { api, ApiError } from "../lib/api";
import { CurrentUser, TOTPSetup } from "../lib/types";

export function Profile() {
  const qc = useQueryClient();
  const me = useQuery({
    queryKey: ["me"],
    queryFn: () => api<CurrentUser>("/v1/auth/me"),
  });

  if (me.isLoading) return <p className="p-6 text-sm text-zinc-400">Loading…</p>;
  if (me.error) return <p className="p-6 text-sm text-fail">{(me.error as Error).message}</p>;
  const user = me.data!;

  return (
    <div className="mx-auto max-w-3xl space-y-8 p-6">
      <header>
        <h2 className="text-lg font-semibold">Profile</h2>
        <p className="text-sm text-zinc-400">
          Signed in as <span className="text-zinc-200">{user.email}</span> ({user.role})
        </p>
      </header>

      <ChangeEmailCard onSuccess={() => qc.invalidateQueries({ queryKey: ["me"] })} />
      <ChangePasswordCard />
      <TwoFactorCard active={user.totp_active} onSuccess={() => qc.invalidateQueries({ queryKey: ["me"] })} />
    </div>
  );
}

function ChangeEmailCard({ onSuccess }: { onSuccess: () => void }) {
  const [pw, setPw] = useState("");
  const [email, setEmail] = useState("");
  const [msg, setMsg] = useState<{ kind: "ok" | "err"; text: string } | null>(null);
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
    <Card title="Change email">
      <form onSubmit={submit} className="space-y-3">
        <Input label="Current password" type="password" value={pw} onChange={setPw} />
        <Input label="New email" type="email" value={email} onChange={setEmail} />
        <FormFooter busy={busy} idle="Update email" busyLabel="Updating…" msg={msg} />
      </form>
    </Card>
  );
}

function ChangePasswordCard() {
  const [cur, setCur] = useState("");
  const [next, setNext] = useState("");
  const [msg, setMsg] = useState<{ kind: "ok" | "err"; text: string } | null>(null);
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
    <Card title="Change password">
      <form onSubmit={submit} className="space-y-3">
        <Input label="Current password" type="password" value={cur} onChange={setCur} />
        <Input label="New password" type="password" value={next} onChange={setNext} />
        <FormFooter busy={busy} idle="Update password" busyLabel="Updating…" msg={msg} />
      </form>
    </Card>
  );
}

function TwoFactorCard({ active, onSuccess }: { active: boolean; onSuccess: () => void }) {
  const [setup, setSetup] = useState<TOTPSetup | null>(null);
  const [code, setCode] = useState("");
  const [pw, setPw] = useState("");
  const [msg, setMsg] = useState<{ kind: "ok" | "err"; text: string } | null>(null);
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
    <Card title={`Two-factor authentication (${active ? "active" : "off"})`}>
      {active ? (
        <form onSubmit={disable} className="space-y-3">
          <p className="text-sm text-zinc-400">
            TOTP is active. To remove, confirm your password.
          </p>
          <Input label="Password" type="password" value={pw} onChange={setPw} />
          <FormFooter busy={busy} idle="Disable 2FA" busyLabel="Disabling…" msg={msg} variant="danger" />
        </form>
      ) : !setup ? (
        <div className="space-y-3">
          <p className="text-sm text-zinc-400">
            Add a second factor by scanning a QR code in your authenticator (Aegis, 1Password, etc.).
          </p>
          <button
            onClick={startSetup}
            disabled={busy}
            className="rounded bg-zinc-100 px-3 py-1.5 text-sm font-medium text-zinc-900 hover:bg-white disabled:opacity-50"
          >
            {busy ? "Generating…" : "Begin setup"}
          </button>
          {msg && <Message msg={msg} />}
        </div>
      ) : (
        <div className="space-y-4">
          <div className="grid gap-4 md:grid-cols-2">
            <div>
              <p className="mb-2 text-xs uppercase tracking-wider text-zinc-400">Scan with authenticator</p>
              <img
                src={`data:image/png;base64,${setup.qr_png_base64}`}
                alt="TOTP QR code"
                className="rounded border border-zinc-700 bg-white p-2"
              />
              <p className="mt-2 text-xs text-zinc-500">
                Or enter the secret manually:
                <code className="ml-1 select-all rounded bg-zinc-800 px-1 py-0.5 font-mono text-xs text-zinc-300">
                  {setup.secret_b32}
                </code>
              </p>
            </div>
            <div>
              <p className="mb-2 text-xs uppercase tracking-wider text-zinc-400">Backup codes (save once!)</p>
              <ul className="grid grid-cols-2 gap-1 rounded border border-zinc-700 bg-zinc-950 p-3 font-mono text-xs">
                {setup.backup_codes.map((c) => (
                  <li key={c} className="select-all">{c}</li>
                ))}
              </ul>
            </div>
          </div>

          <form onSubmit={verify} className="space-y-3">
            <Input label="Confirm with a current 6-digit code" type="text" value={code} onChange={setCode} className="font-mono tracking-widest" />
            <FormFooter busy={busy} idle="Activate 2FA" busyLabel="Verifying…" msg={msg} />
          </form>
        </div>
      )}
    </Card>
  );
}

// ---- shared (private to this module — outside callers should use the
// primitives in components/ui instead) ----

function Card({ title, children }: { title: string; children: React.ReactNode }) {
  return (
    <section className="rounded-lg border border-zinc-800 bg-zinc-900 p-5">
      <h3 className="mb-4 text-sm font-semibold text-zinc-200">{title}</h3>
      {children}
    </section>
  );
}

function Input(props: {
  label: string;
  type: string;
  value: string;
  onChange: (v: string) => void;
  className?: string;
  required?: boolean;
}) {
  return (
    <label className="block">
      <span className="text-xs text-zinc-400">{props.label}</span>
      <input
        type={props.type}
        required={props.required ?? true}
        value={props.value}
        onChange={(e) => props.onChange(e.target.value)}
        className={`mt-1 w-full rounded border border-zinc-700 bg-zinc-950 px-3 py-1.5 text-sm focus:border-zinc-500 focus:outline-none ${props.className ?? ""}`}
      />
    </label>
  );
}

function Message({ msg }: { msg: { kind: "ok" | "err"; text: string } }) {
  return (
    <p
      className={`rounded px-3 py-2 text-sm ${
        msg.kind === "ok"
          ? "border border-ok/40 bg-ok/10 text-ok"
          : "border border-fail/40 bg-fail/10 text-fail"
      }`}
    >
      {msg.text}
    </p>
  );
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
  msg: { kind: "ok" | "err"; text: string } | null;
  variant?: "danger";
}) {
  const classes =
    variant === "danger"
      ? "rounded bg-fail/20 px-3 py-1.5 text-sm font-medium text-fail border border-fail/40 hover:bg-fail/30 disabled:opacity-50"
      : "rounded bg-zinc-100 px-3 py-1.5 text-sm font-medium text-zinc-900 hover:bg-white disabled:opacity-50";
  return (
    <div className="space-y-2">
      <button type="submit" disabled={busy} className={classes}>
        {busy ? busyLabel : idle}
      </button>
      {msg && <Message msg={msg} />}
    </div>
  );
}
