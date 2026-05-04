import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { Mail, Send } from "lucide-react";
import { ChangeEvent, FormEvent, useEffect, useState } from "react";

import {
  Button,
  ErrorBox,
  Field,
  Panel,
  PanelBody,
  PanelHeader,
  SuccessBox,
  TextInput,
} from "../components/ui";
import { api, ApiError } from "../lib/api";
import { useAuth } from "../lib/auth";
import type { SmtpSettings, SmtpSettingsInput } from "../lib/types";

type Msg = { kind: "ok" | "err"; text: string } | null;

type EncryptionMode = "none" | "starttls" | "tls";

function deriveEncryption(starttls: boolean, tls: boolean): EncryptionMode {
  if (tls) return "tls";
  if (starttls) return "starttls";
  return "none";
}

export function AdminMail() {
  const qc = useQueryClient();
  const myEmail = useAuth((s) => s.user?.email ?? "");
  const settings = useQuery({
    queryKey: ["admin-smtp"],
    queryFn: () => api<SmtpSettings>("/v1/admin/mail"),
  });

  return (
    <div className="mx-auto max-w-3xl space-y-6 p-6">
      <header>
        <h2 className="flex items-center gap-2 text-lg font-semibold">
          <Mail className="h-4 w-4 text-accent" /> Mail (SMTP)
        </h2>
        <p className="text-sm text-fg-muted">
          One global SMTP transport. Every email-typed notification channel reuses these settings — users
          only choose a recipient address.
        </p>
      </header>

      {settings.isLoading ? (
        <p className="text-sm text-fg-muted">Loading…</p>
      ) : settings.error ? (
        <ErrorBox>{(settings.error as Error).message}</ErrorBox>
      ) : (
        <SettingsForm
          initial={settings.data!}
          onSaved={() => qc.invalidateQueries({ queryKey: ["admin-smtp"] })}
        />
      )}

      {settings.data && settings.data.host && <TestCard defaultTo={myEmail} />}
    </div>
  );
}

function SettingsForm({ initial, onSaved }: { initial: SmtpSettings; onSaved: () => void }) {
  const [host, setHost] = useState(initial.host);
  const [port, setPort] = useState(initial.port || 587);
  const [username, setUsername] = useState(initial.username);
  const [password, setPassword] = useState("");
  const [clearPassword, setClearPassword] = useState(false);
  const [fromAddress, setFromAddress] = useState(initial.from_address);
  const [encryption, setEncryption] = useState<EncryptionMode>(
    deriveEncryption(initial.starttls, initial.tls),
  );
  const [insecureSkipVerify, setInsecureSkipVerify] = useState(initial.insecure_skip_verify);
  const [msg, setMsg] = useState<Msg>(null);

  const starttls = encryption === "starttls";
  const tls = encryption === "tls";

  useEffect(() => {
    setHost(initial.host);
    setPort(initial.port || 587);
    setUsername(initial.username);
    setFromAddress(initial.from_address);
    setEncryption(deriveEncryption(initial.starttls, initial.tls));
    setInsecureSkipVerify(initial.insecure_skip_verify);
  }, [initial]);

  const save = useMutation({
    mutationFn: () => {
      const body: SmtpSettingsInput = {
        host,
        port: Number(port) || 587,
        username,
        password,
        clear_password: clearPassword,
        from_address: fromAddress,
        starttls,
        tls,
        insecure_skip_verify: insecureSkipVerify,
      };
      return api<SmtpSettings>("/v1/admin/mail", {
        method: "PUT",
        body: JSON.stringify(body),
      });
    },
    onSuccess: () => {
      setMsg({ kind: "ok", text: "Settings saved." });
      setPassword("");
      setClearPassword(false);
      onSaved();
    },
    onError: (err) =>
      setMsg({ kind: "err", text: err instanceof ApiError ? err.detail : "save failed" }),
  });

  function submit(e: FormEvent) {
    e.preventDefault();
    if (clearPassword && password === "") {
      const ok = window.confirm(
        "Wipe the stored SMTP password? Outbound mail will fail until you set a new one.",
      );
      if (!ok) return;
    }
    setMsg(null);
    save.mutate();
  }

  return (
    <Panel>
      <PanelHeader>
        <h3 className="text-sm font-semibold text-fg">SMTP transport</h3>
      </PanelHeader>
      <PanelBody>
        <form onSubmit={submit} className="space-y-4">
          <div className="grid grid-cols-1 gap-3 md:grid-cols-2">
            <Field label="Host">
              <TextInput
                type="text"
                required
                value={host}
                onChange={(e: ChangeEvent<HTMLInputElement>) => setHost(e.target.value)}
                placeholder="smtp.example.com"
              />
            </Field>
            <Field label="Port">
              <TextInput
                type="number"
                required
                min={1}
                max={65535}
                value={port}
                onChange={(e) => setPort(Number(e.target.value))}
              />
            </Field>
            <Field label="From address">
              <TextInput
                type="email"
                required
                value={fromAddress}
                onChange={(e) => setFromAddress(e.target.value)}
                placeholder="alerts@example.com"
              />
            </Field>
            <Field label="Username" hint="Optional, only set if your relay needs auth.">
              <TextInput
                type="text"
                value={username}
                onChange={(e) => setUsername(e.target.value)}
              />
            </Field>
            <Field
              label={initial.has_password ? "Password (leave blank to keep stored)" : "Password"}
            >
              <TextInput
                type="password"
                value={password}
                onChange={(e) => setPassword(e.target.value)}
                placeholder={initial.has_password ? "********" : ""}
                disabled={clearPassword}
                className="font-mono"
              />
            </Field>
            {initial.has_password ? (
              <label className="flex items-center gap-2 self-end pb-2 text-sm text-fg">
                <input
                  type="checkbox"
                  checked={clearPassword}
                  onChange={(e) => {
                    setClearPassword(e.target.checked);
                    if (e.target.checked) setPassword("");
                  }}
                />
                Wipe stored password
              </label>
            ) : (
              <span />
            )}
          </div>

          <fieldset className="space-y-2 rounded-md border border-border p-3 text-sm">
            <legend className="px-1 text-xs uppercase tracking-wide text-fg-muted">Encryption</legend>
            <label className="flex items-center gap-2">
              <input
                type="radio"
                name="encryption"
                value="none"
                checked={encryption === "none"}
                onChange={() => setEncryption("none")}
              />
              None
            </label>
            <label className="flex items-center gap-2">
              <input
                type="radio"
                name="encryption"
                value="starttls"
                checked={encryption === "starttls"}
                onChange={() => setEncryption("starttls")}
              />
              STARTTLS (port 587)
            </label>
            <label className="flex items-center gap-2">
              <input
                type="radio"
                name="encryption"
                value="tls"
                checked={encryption === "tls"}
                onChange={() => setEncryption("tls")}
              />
              Implicit TLS (port 465)
            </label>
            <label
              className={`flex items-center gap-2 ${encryption === "none" ? "opacity-50" : ""}`}
            >
              <input
                type="checkbox"
                checked={insecureSkipVerify}
                disabled={encryption === "none"}
                onChange={(e) => setInsecureSkipVerify(e.target.checked)}
              />
              <span>
                Skip TLS certificate verification{" "}
                <span className="text-fail">(dangerous; only for self-signed dev mailservers)</span>
              </span>
            </label>
            <p className="px-1 text-xs text-fg-muted">
              Only relevant for STARTTLS or Implicit TLS modes.
            </p>
          </fieldset>

          {msg &&
            (msg.kind === "ok" ? (
              <SuccessBox>{msg.text}</SuccessBox>
            ) : (
              <ErrorBox>{msg.text}</ErrorBox>
            ))}

          <div className="flex items-center gap-3">
            <Button type="submit" variant="primary" disabled={save.isPending}>
              {save.isPending ? "Saving…" : "Save settings"}
            </Button>
            {initial.updated_at && (
              <span className="text-xs text-fg-subtle">
                Last updated {new Date(initial.updated_at).toLocaleString()}
                {initial.updated_by ? ` by ${initial.updated_by}` : ""}
              </span>
            )}
          </div>
        </form>
      </PanelBody>
    </Panel>
  );
}

function TestCard({ defaultTo }: { defaultTo: string }) {
  const [to, setTo] = useState(defaultTo);
  const [msg, setMsg] = useState<Msg>(null);

  const send = useMutation({
    mutationFn: () =>
      api<{ ok: boolean; error?: string }>("/v1/admin/mail/test", {
        method: "POST",
        body: JSON.stringify({ to }),
      }),
    onSuccess: (data) => {
      if (data.ok) setMsg({ kind: "ok", text: `Test mail dispatched to ${to}.` });
      else setMsg({ kind: "err", text: data.error || "test failed" });
    },
    onError: (err) =>
      setMsg({ kind: "err", text: err instanceof ApiError ? err.detail : "test failed" }),
  });

  return (
    <Panel>
      <PanelHeader>
        <h3 className="text-sm font-semibold text-fg">Send test message</h3>
      </PanelHeader>
      <PanelBody>
        <form
          onSubmit={(e) => {
            e.preventDefault();
            setMsg(null);
            send.mutate();
          }}
          className="space-y-3"
        >
          <Field label="Recipient">
            <TextInput
              type="email"
              required
              value={to}
              onChange={(e) => setTo(e.target.value)}
            />
          </Field>
          {msg &&
            (msg.kind === "ok" ? (
              <SuccessBox>{msg.text}</SuccessBox>
            ) : (
              <ErrorBox>{msg.text}</ErrorBox>
            ))}
          <Button type="submit" disabled={send.isPending}>
            <Send className="h-3.5 w-3.5" />
            {send.isPending ? "Sending…" : "Send test mail"}
          </Button>
        </form>
      </PanelBody>
    </Panel>
  );
}
