import { useQuery, useQueryClient } from "@tanstack/react-query";
import { ChangeEvent, FormEvent, ReactNode, useState } from "react";

import {
  Button,
  ErrorBox,
  Field,
  Panel,
  PanelBody,
  PanelHeader,
  Skeleton,
  SuccessBox,
  TextInput,
} from "../components/ui";
import { api, ApiError } from "../lib/api";
import { DensityProvider, useDensityStore, type Density } from "../lib/density-store";
import { CurrentUser, TOTPSetup } from "../lib/types";

type Msg = { kind: "ok" | "err"; text: string } | null;

export function Profile() {
  const qc = useQueryClient();
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

  return (
    <div className="mx-auto max-w-3xl space-y-6 p-6">
      {/* Mount the html[data-density] side effect from this page. The
          provider is a no-op render — it just mirrors the persisted store
          value onto <html>. Remove once App.tsx (Phase A) hosts it. */}
      <DensityProvider />
      <header>
        <h2 className="text-lg font-semibold text-fg">Profile</h2>
        <p className="text-sm text-fg-muted">
          Signed in as <span className="text-fg">{user.email}</span> ({user.role})
        </p>
      </header>

      <ChangeEmailCard onSuccess={() => qc.invalidateQueries({ queryKey: ["me"] })} />
      <ChangePasswordCard />
      <TwoFactorCard active={user.totp_active} onSuccess={() => qc.invalidateQueries({ queryKey: ["me"] })} />
      <DisplayCard />
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
