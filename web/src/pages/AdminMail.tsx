import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { Mail, Send } from "lucide-react";
import { ChangeEvent, FormEvent, ReactNode, useEffect, useState } from "react";

import { api, ApiError } from "../lib/api";
import { useAuth } from "../lib/auth";
import { Card } from "./Profile";
import type { SmtpSettings, SmtpSettingsInput } from "../lib/types";

type Msg = { kind: "ok" | "err"; text: string } | null;

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
        <p className="text-sm text-zinc-400">
          One global SMTP transport. Every email-typed notification channel reuses these settings — users
          only choose a recipient address.
        </p>
      </header>

      {settings.isLoading ? (
        <p className="text-sm text-zinc-400">Loading…</p>
      ) : settings.error ? (
        <p className="text-sm text-fail">{(settings.error as Error).message}</p>
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

function Field({
  label,
  hint,
  children,
}: {
  label: string;
  hint?: string;
  children: ReactNode;
}) {
  return (
    <label className="block">
      <span className="text-xs text-zinc-400">{label}</span>
      {children}
      {hint && <span className="mt-1 block text-[11px] text-zinc-500">{hint}</span>}
    </label>
  );
}

function inputClass(extra = "") {
  return `mt-1 w-full rounded border border-zinc-700 bg-zinc-950 px-3 py-1.5 text-sm focus:border-zinc-500 focus:outline-none disabled:opacity-50 ${extra}`;
}

function SettingsForm({ initial, onSaved }: { initial: SmtpSettings; onSaved: () => void }) {
  const [host, setHost] = useState(initial.host);
  const [port, setPort] = useState(initial.port || 587);
  const [username, setUsername] = useState(initial.username);
  const [password, setPassword] = useState("");
  const [clearPassword, setClearPassword] = useState(false);
  const [fromAddress, setFromAddress] = useState(initial.from_address);
  const [starttls, setStarttls] = useState(initial.starttls);
  const [tls, setTls] = useState(initial.tls);
  const [insecureSkipVerify, setInsecureSkipVerify] = useState(initial.insecure_skip_verify);
  const [msg, setMsg] = useState<Msg>(null);

  useEffect(() => {
    setHost(initial.host);
    setPort(initial.port || 587);
    setUsername(initial.username);
    setFromAddress(initial.from_address);
    setStarttls(initial.starttls);
    setTls(initial.tls);
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
    setMsg(null);
    save.mutate();
  }

  return (
    <Card title="SMTP transport">
      <form onSubmit={submit} className="space-y-4">
        <div className="grid grid-cols-1 gap-3 md:grid-cols-2">
          <Field label="Host">
            <input
              type="text"
              required
              value={host}
              onChange={(e: ChangeEvent<HTMLInputElement>) => setHost(e.target.value)}
              placeholder="smtp.example.com"
              className={inputClass()}
            />
          </Field>
          <Field label="Port">
            <input
              type="number"
              required
              min={1}
              max={65535}
              value={port}
              onChange={(e) => setPort(Number(e.target.value))}
              className={inputClass()}
            />
          </Field>
          <Field label="From address">
            <input
              type="email"
              required
              value={fromAddress}
              onChange={(e) => setFromAddress(e.target.value)}
              placeholder="alerts@example.com"
              className={inputClass()}
            />
          </Field>
          <Field label="Username" hint="Optional, only set if your relay needs auth.">
            <input
              type="text"
              value={username}
              onChange={(e) => setUsername(e.target.value)}
              className={inputClass()}
            />
          </Field>
          <Field
            label={initial.has_password ? "Password (leave blank to keep stored)" : "Password"}
          >
            <input
              type="password"
              value={password}
              onChange={(e) => setPassword(e.target.value)}
              placeholder={initial.has_password ? "********" : ""}
              disabled={clearPassword}
              className={inputClass("font-mono")}
            />
          </Field>
          {initial.has_password ? (
            <label className="flex items-center gap-2 self-end pb-2 text-sm text-zinc-300">
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

        <fieldset className="space-y-2 rounded-md border border-zinc-700 p-3 text-sm">
          <legend className="px-1 text-xs uppercase tracking-wide text-zinc-400">Encryption</legend>
          <label className="flex items-center gap-2">
            <input type="checkbox" checked={starttls} onChange={(e) => setStarttls(e.target.checked)} />
            STARTTLS (port 587 default)
          </label>
          <label className="flex items-center gap-2">
            <input type="checkbox" checked={tls} onChange={(e) => setTls(e.target.checked)} />
            Implicit TLS (port 465)
          </label>
          <label className="flex items-center gap-2">
            <input
              type="checkbox"
              checked={insecureSkipVerify}
              onChange={(e) => setInsecureSkipVerify(e.target.checked)}
            />
            <span>
              Skip TLS certificate verification{" "}
              <span className="text-fail">(dangerous; only for self-signed dev mailservers)</span>
            </span>
          </label>
        </fieldset>

        {msg && (
          <p className={`text-sm ${msg.kind === "ok" ? "text-ok" : "text-fail"}`}>{msg.text}</p>
        )}

        <div className="flex items-center gap-3">
          <button
            type="submit"
            disabled={save.isPending}
            className="rounded-md bg-accent px-3 py-1.5 text-sm font-medium text-zinc-950 disabled:opacity-50"
          >
            {save.isPending ? "Saving…" : "Save settings"}
          </button>
          {initial.updated_at && (
            <span className="text-xs text-zinc-500">
              Last updated {new Date(initial.updated_at).toLocaleString()}
              {initial.updated_by ? ` by ${initial.updated_by}` : ""}
            </span>
          )}
        </div>
      </form>
    </Card>
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
    <Card title="Send test message">
      <form
        onSubmit={(e) => {
          e.preventDefault();
          setMsg(null);
          send.mutate();
        }}
        className="space-y-3"
      >
        <Field label="Recipient">
          <input
            type="email"
            required
            value={to}
            onChange={(e) => setTo(e.target.value)}
            className={inputClass()}
          />
        </Field>
        {msg && (
          <p className={`text-sm ${msg.kind === "ok" ? "text-ok" : "text-fail"}`}>{msg.text}</p>
        )}
        <button
          type="submit"
          disabled={send.isPending}
          className="inline-flex items-center gap-1 rounded-md bg-zinc-700 px-3 py-1.5 text-sm font-medium text-zinc-100 disabled:opacity-50"
        >
          <Send className="h-3.5 w-3.5" />
          {send.isPending ? "Sending…" : "Send test mail"}
        </button>
      </form>
    </Card>
  );
}
