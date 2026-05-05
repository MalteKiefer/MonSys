import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { Check, ChevronLeft, ChevronRight, Mail, Send, ShieldCheck, UserRound, Wifi } from "lucide-react";
import { ChangeEvent, FormEvent, useEffect, useState } from "react";

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
    <Page
      title={
        <span className="flex items-center gap-2">
          <Mail className="h-5 w-5 text-accent" /> Mail (SMTP)
        </span>
      }
      subtitle="One global SMTP transport. Every email-typed notification channel reuses these settings — users only choose a recipient address."
    >
      {settings.isLoading ? (
        <Skeleton className="h-64" />
      ) : settings.error ? (
        <ErrorBox>{(settings.error as Error).message}</ErrorBox>
      ) : (
        <SettingsWizard
          initial={settings.data!}
          onSaved={() => {
            qc.invalidateQueries({ queryKey: ["admin-smtp"] });
            qc.invalidateQueries({ queryKey: ["auth-config"] });
          }}
        />
      )}

      {settings.data && settings.data.host && <TestCard defaultTo={myEmail} />}
    </Page>
  );
}

// ---- Stepper ---------------------------------------------------------------

type StepKey = "transport" | "identity";

const STEPS: { key: StepKey; label: string; icon: typeof Wifi }[] = [
  { key: "transport", label: "Transport", icon: Wifi },
  { key: "identity", label: "Identity", icon: UserRound },
];

function Stepper({ current, completed }: { current: StepKey; completed: Set<StepKey> }) {
  return (
    <ol className="flex items-center gap-2 text-sm">
      {STEPS.map((s, idx) => {
        const isCurrent = current === s.key;
        const isDone = completed.has(s.key);
        const Icon = s.icon;
        const stateCls = isCurrent
          ? "border-accent bg-accent/10 text-accent"
          : isDone
            ? "border-ok/40 bg-ok/10 text-ok"
            : "border-border bg-panel text-fg-subtle";
        return (
          <li key={s.key} className="flex items-center gap-2">
            <span
              className={`inline-flex h-7 w-7 items-center justify-center rounded-full border ${stateCls}`}
              aria-current={isCurrent ? "step" : undefined}
            >
              {isDone && !isCurrent ? <Check className="h-3.5 w-3.5" /> : <Icon className="h-3.5 w-3.5" />}
            </span>
            <span className={isCurrent ? "font-medium text-fg" : "text-fg-muted"}>
              {idx + 1}. {s.label}
            </span>
            {idx < STEPS.length - 1 && (
              <span className="ml-2 inline-block h-px w-6 bg-border" aria-hidden />
            )}
          </li>
        );
      })}
    </ol>
  );
}

// ---- Wizard ----------------------------------------------------------------

function SettingsWizard({ initial, onSaved }: { initial: SmtpSettings; onSaved: () => void }) {
  const myEmail = useAuth((s) => s.user?.email ?? "");

  // Step 1 — transport
  const [host, setHost] = useState(initial.host);
  const [port, setPort] = useState<number>(initial.port || 587);
  const [encryption, setEncryption] = useState<EncryptionMode>(
    deriveEncryption(initial.starttls, initial.tls),
  );
  const [insecureSkipVerify, setInsecureSkipVerify] = useState(initial.insecure_skip_verify);

  // Step 2 — identity
  const [username, setUsername] = useState(initial.username);
  const [password, setPassword] = useState("");
  const [clearPassword, setClearPassword] = useState(false);
  const [fromAddress, setFromAddress] = useState(initial.from_address);

  const [step, setStep] = useState<StepKey>("transport");
  const [stepErrors, setStepErrors] = useState<string[]>([]);
  const [completed, setCompleted] = useState<Set<StepKey>>(new Set());
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
      setCompleted(new Set(["transport", "identity"]));
      onSaved();
    },
    onError: (err) =>
      setMsg({ kind: "err", text: err instanceof ApiError ? err.detail : "save failed" }),
  });

  // Round-trip transport check using the existing test endpoint. Only enabled
  // once the persisted settings have a host (otherwise the server has nothing
  // to dial). The button is a UX shortcut — it sends to the operator's own
  // email so they can verify the wire-level behaviour without filling out the
  // dedicated test form below.
  const testConn = useMutation({
    mutationFn: () =>
      api<{ ok: boolean; error?: string }>("/v1/admin/mail/test", {
        method: "POST",
        body: JSON.stringify({ to: myEmail }),
      }),
    onSuccess: (data) => {
      if (data.ok) setMsg({ kind: "ok", text: `Connection OK — test mail dispatched to ${myEmail}.` });
      else setMsg({ kind: "err", text: data.error || "transport test failed" });
    },
    onError: (err) =>
      setMsg({ kind: "err", text: err instanceof ApiError ? err.detail : "transport test failed" }),
  });

  function validateTransport(): string[] {
    const errs: string[] = [];
    if (!host.trim()) errs.push("Host is required.");
    if (!port || port < 1 || port > 65535) errs.push("Port must be between 1 and 65535.");
    return errs;
  }

  function validateIdentity(): string[] {
    const errs: string[] = [];
    if (!fromAddress.trim()) errs.push("From-address is required.");
    if (fromAddress && !/^[^\s@]+@[^\s@]+\.[^\s@]+$/.test(fromAddress)) {
      errs.push("From-address must look like an email.");
    }
    return errs;
  }

  function next() {
    const errs = validateTransport();
    if (errs.length > 0) {
      setStepErrors(errs);
      return;
    }
    setStepErrors([]);
    setCompleted((s) => new Set([...s, "transport"]));
    setStep("identity");
  }

  function back() {
    setStepErrors([]);
    setStep("transport");
  }

  function submit(e: FormEvent) {
    e.preventDefault();
    const tErrs = validateTransport();
    const iErrs = validateIdentity();
    if (tErrs.length > 0) {
      setStep("transport");
      setStepErrors(tErrs);
      return;
    }
    if (iErrs.length > 0) {
      setStepErrors(iErrs);
      return;
    }
    if (clearPassword && password === "") {
      const ok = window.confirm(
        "Wipe the stored SMTP password? Outbound mail will fail until you set a new one.",
      );
      if (!ok) return;
    }
    setStepErrors([]);
    setMsg(null);
    save.mutate();
  }

  const securityHint = (() => {
    if (encryption === "tls") return "Implicit TLS — full TLS handshake on connect (typically port 465).";
    if (encryption === "starttls") return "STARTTLS — upgrade plain connection to TLS (typically port 587).";
    return "Insecure — clear-text SMTP. Only acceptable for in-cluster relays.";
  })();

  return (
    <Panel>
      <PanelHeader>
        <h3 className="text-sm font-semibold text-fg">SMTP transport</h3>
        <Stepper current={step} completed={completed} />
      </PanelHeader>
      <PanelBody>
        <form onSubmit={submit} className="space-y-4">
          {step === "transport" && (
            <div className="space-y-4">
              <div className="grid grid-cols-1 gap-3 md:grid-cols-2">
                <Field label="Host">
                  <TextInput
                    type="text"
                    required
                    value={host}
                    onChange={(e: ChangeEvent<HTMLInputElement>) => setHost(e.target.value)}
                    placeholder="smtp.example.com"
                    autoFocus
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
              </div>

              <fieldset className="space-y-2 rounded-md border border-border p-3 text-sm">
                <legend className="px-1 text-xs uppercase tracking-wide text-fg-muted">
                  Security mode
                </legend>
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
                <label className="flex items-center gap-2">
                  <input
                    type="radio"
                    name="encryption"
                    value="none"
                    checked={encryption === "none"}
                    onChange={() => setEncryption("none")}
                  />
                  Insecure (no TLS)
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
                    <span className="text-fail">
                      (dangerous; only for self-signed dev mailservers)
                    </span>
                  </span>
                </label>
                <p className="flex items-center gap-1.5 px-1 pt-1 text-xs text-fg-muted">
                  <ShieldCheck className="h-3 w-3" />
                  {securityHint}
                </p>
              </fieldset>

              <div className="flex items-center gap-2">
                <Button
                  type="button"
                  onClick={() => {
                    setMsg(null);
                    testConn.mutate();
                  }}
                  disabled={!initial.host || testConn.isPending || !myEmail}
                  title={
                    !initial.host
                      ? "Save settings once before running a transport test."
                      : !myEmail
                        ? "Your account has no email on file."
                        : `Send a probe mail to ${myEmail} using the saved transport.`
                  }
                >
                  <Wifi className="h-3.5 w-3.5" />
                  {testConn.isPending ? "Testing…" : "Test connection"}
                </Button>
                <span className="text-xs text-fg-subtle">
                  Uses the persisted transport. Save first if this is the initial setup.
                </span>
              </div>
            </div>
          )}

          {step === "identity" && (
            <div className="space-y-4">
              <div className="grid grid-cols-1 gap-3 md:grid-cols-2">
                <Field label="Username" hint="Optional, only set if your relay needs auth.">
                  <TextInput
                    type="text"
                    value={username}
                    onChange={(e) => setUsername(e.target.value)}
                    autoFocus
                  />
                </Field>
                <Field
                  label={
                    initial.has_password ? "Password (leave blank to keep stored)" : "Password"
                  }
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
                <Field label="From address">
                  <TextInput
                    type="email"
                    required
                    value={fromAddress}
                    onChange={(e) => setFromAddress(e.target.value)}
                    placeholder="alerts@example.com"
                  />
                </Field>
                {initial.has_password && (
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
                )}
              </div>

              <div className="rounded-md border border-border bg-panel-2 p-3 text-xs text-fg-muted">
                <div className="mb-1 flex items-center gap-1.5 font-medium text-fg">
                  <Check className="h-3.5 w-3.5 text-ok" /> Review
                </div>
                <dl className="grid grid-cols-2 gap-x-4 gap-y-1 font-mono">
                  <dt className="text-fg-subtle">host</dt>
                  <dd>{host || "—"}</dd>
                  <dt className="text-fg-subtle">port</dt>
                  <dd>{port}</dd>
                  <dt className="text-fg-subtle">security</dt>
                  <dd>
                    <StatusPill status={encryption === "none" ? "warn" : "ok"}>
                      {encryption}
                    </StatusPill>
                  </dd>
                  <dt className="text-fg-subtle">skip-verify</dt>
                  <dd>{insecureSkipVerify ? "yes" : "no"}</dd>
                </dl>
              </div>
            </div>
          )}

          {stepErrors.length > 0 && (
            <ErrorBox>
              {stepErrors.length === 1 ? (
                stepErrors[0]
              ) : (
                <ul className="list-disc pl-5">
                  {stepErrors.map((e) => (
                    <li key={e}>{e}</li>
                  ))}
                </ul>
              )}
            </ErrorBox>
          )}

          {msg &&
            (msg.kind === "ok" ? (
              <SuccessBox>{msg.text}</SuccessBox>
            ) : (
              <ErrorBox>{msg.text}</ErrorBox>
            ))}

          <div className="flex flex-wrap items-center gap-3">
            {step === "transport" ? (
              <Button type="button" variant="primary" onClick={next}>
                Next: Identity <ChevronRight className="h-3.5 w-3.5" />
              </Button>
            ) : (
              <>
                <Button type="button" onClick={back}>
                  <ChevronLeft className="h-3.5 w-3.5" /> Back
                </Button>
                <Button type="submit" variant="primary" disabled={save.isPending}>
                  {save.isPending ? "Saving…" : "Save"}
                </Button>
              </>
            )}
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
