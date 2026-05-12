import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import {
  Activity,
  Check,
  ChevronLeft,
  ChevronRight,
  Mail,
  Send,
  ShieldCheck,
  UserRound,
  Wifi,
} from "lucide-react";
import type { ChangeEvent, SyntheticEvent} from "react";
import { useEffect, useMemo, useState } from "react";

import { Page } from "../components/page";
import type {
  TabItem} from "../components/ui";
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
  Tabs,
  TextInput,
} from "../components/ui";
import { useT } from "../i18n/useT";
import { api, ApiError } from "../lib/api";
import { useAuth } from "../lib/auth";
import type { SmtpSettings, SmtpSettingsInput } from "../lib/types";

type Msg = { kind: "ok" | "err"; text: string } | null;

// Last observed outcome of either the "transport probe" (server tab) or the
// dedicated send-test (test tab). Lifted into the parent so the status tab can
// summarise it without re-issuing a network call.
interface TestOutcome {
  kind: "ok" | "err";
  text: string;
  at: string; // ISO timestamp
  source: "transport-probe" | "send-test";
}

type EncryptionMode = "none" | "starttls" | "tls";

type TabKey = "server" | "test" | "status";

function deriveEncryption(starttls: boolean, tls: boolean): EncryptionMode {
  if (tls) return "tls";
  if (starttls) return "starttls";
  return "none";
}

export function AdminMail() {
  const { t } = useT(["admin", "common"]);
  const qc = useQueryClient();
  const myEmail = useAuth((s) => s.user?.email ?? "");
  const settings = useQuery({
    queryKey: ["admin-smtp"],
    queryFn: () => api<SmtpSettings>("/v1/admin/mail"),
  });

  const [tab, setTab] = useState<TabKey>("server");
  const [lastOutcome, setLastOutcome] = useState<TestOutcome | null>(null);

  const tabs: readonly TabItem<TabKey>[] = useMemo(
    () => [
      { key: "server", label: t("admin:mail.tabs.server"), icon: Mail },
      { key: "test", label: t("admin:mail.tabs.test"), icon: Send },
      { key: "status", label: t("admin:mail.tabs.status"), icon: Activity },
    ],
    [t],
  );

  return (
    <Page
      title={
        <span className="flex items-center gap-2">
          <Mail className="h-5 w-5 text-accent" /> {t("admin:mail.title")}
        </span>
      }
      subtitle={t("admin:mail.subtitle")}
    >
      <Tabs<TabKey> items={tabs} value={tab} onChange={setTab} />

      {settings.isLoading ? (
        <div id="panel-server" role="tabpanel" className="pt-4">
          <Skeleton className="h-64" />
        </div>
      ) : settings.error ? (
        <div id="panel-server" role="tabpanel" className="pt-4">
          <ErrorBox>{(settings.error).message}</ErrorBox>
        </div>
      ) : settings.data ? (
        <div className="pt-4">
          {tab === "server" && (
            <div id="panel-server" role="tabpanel" aria-labelledby="tab-server">
              <SettingsWizard
                initial={settings.data}
                onSaved={() => {
                  void qc.invalidateQueries({ queryKey: ["admin-smtp"] });
                  void qc.invalidateQueries({ queryKey: ["auth-config"] });
                }}
                onOutcome={setLastOutcome}
              />
            </div>
          )}

          {tab === "test" && (
            <div id="panel-test" role="tabpanel" aria-labelledby="tab-test">
              {settings.data?.host ? (
                <TestCard defaultTo={myEmail} onOutcome={setLastOutcome} />
              ) : (
                <Panel>
                  <PanelHeader>
                    <h3 className="text-sm font-semibold text-fg">{t("admin:mail.test.title")}</h3>
                  </PanelHeader>
                  <PanelBody>
                    <p className="text-sm text-fg-muted">
                      {t("admin:mail.test.lockedHint")}
                    </p>
                  </PanelBody>
                </Panel>
              )}
            </div>
          )}

          {tab === "status" && (
            <div id="panel-status" role="tabpanel" aria-labelledby="tab-status">
              <StatusCard settings={settings.data} lastOutcome={lastOutcome} />
            </div>
          )}
        </div>
      ) : null}
    </Page>
  );
}

// ---- Stepper ---------------------------------------------------------------

type StepKey = "transport" | "identity";

function Stepper({ current, completed }: { current: StepKey; completed: Set<StepKey> }) {
  const { t } = useT(["admin"]);
  const steps: { key: StepKey; label: string; icon: typeof Wifi }[] = [
    { key: "transport", label: t("admin:mail.steps.transport"), icon: Wifi },
    { key: "identity", label: t("admin:mail.steps.identity"), icon: UserRound },
  ];
  return (
    <ol className="flex items-center gap-2 text-sm">
      {steps.map((s, idx) => {
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
              {t("admin:mail.steps.stepLabel", { index: idx + 1, label: s.label })}
            </span>
            {idx < steps.length - 1 && (
              <span className="ml-2 inline-block h-px w-6 bg-border" aria-hidden />
            )}
          </li>
        );
      })}
    </ol>
  );
}

// ---- Wizard ----------------------------------------------------------------

function SettingsWizard({
  initial,
  onSaved,
  onOutcome,
}: {
  initial: SmtpSettings;
  onSaved: () => void;
  onOutcome: (o: TestOutcome) => void;
}) {
  const { t } = useT(["admin", "common"]);
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

  // Re-seed the form fields when `initial` changes (e.g. settings query
  // refetches after a save). State is intentionally derived from a prop here
  // because each form field is locally editable; we can't pass the value
  // straight through. The set-state-in-effect rule's preferred alternatives
  // (key-resetting / useSyncExternalStore) don't fit a multi-field draft.
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
        port: port || 587,
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
      setMsg({ kind: "ok", text: t("admin:mail.wizard.saved") });
      setPassword("");
      setClearPassword(false);
      setCompleted(new Set(["transport", "identity"]));
      onSaved();
    },
    onError: (err) =>
      { setMsg({
        kind: "err",
        text: err instanceof ApiError ? err.detail : t("admin:mail.wizard.saveFailed"),
      }); },
  });

  // Round-trip transport check using the existing test endpoint. Only enabled
  // once the persisted settings have a host (otherwise the server has nothing
  // to dial). The button is a UX shortcut — it sends to the operator's own
  // email so they can verify the wire-level behaviour without filling out the
  // dedicated test form in the Send-test tab.
  const testConn = useMutation({
    mutationFn: () =>
      api<{ ok: boolean; error?: string }>("/v1/admin/mail/test", {
        method: "POST",
        body: JSON.stringify({ to: myEmail }),
      }),
    onSuccess: (data) => {
      if (data.ok) {
        const text = t("admin:mail.test.transportDispatched", { email: myEmail });
        setMsg({ kind: "ok", text });
        onOutcome({ kind: "ok", text, at: new Date().toISOString(), source: "transport-probe" });
      } else {
        const text = data.error || t("admin:mail.test.transportFailed");
        setMsg({ kind: "err", text });
        onOutcome({ kind: "err", text, at: new Date().toISOString(), source: "transport-probe" });
      }
    },
    onError: (err) => {
      const text = err instanceof ApiError ? err.detail : t("admin:mail.test.transportFailed");
      setMsg({ kind: "err", text });
      onOutcome({ kind: "err", text, at: new Date().toISOString(), source: "transport-probe" });
    },
  });

  function validateTransport(): string[] {
    const errs: string[] = [];
    if (!host.trim()) errs.push(t("admin:mail.wizard.validation.hostRequired"));
    if (!port || port < 1 || port > 65535) errs.push(t("admin:mail.wizard.validation.portRange"));
    return errs;
  }

  function validateIdentity(): string[] {
    const errs: string[] = [];
    if (!fromAddress.trim()) errs.push(t("admin:mail.wizard.validation.fromRequired"));
    if (fromAddress && !/^[^\s@]+@[^\s@]+\.[^\s@]+$/.test(fromAddress)) {
      errs.push(t("admin:mail.wizard.validation.fromEmail"));
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

  function submit(e: SyntheticEvent) {
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
      const ok = window.confirm(t("admin:mail.wizard.confirmWipe"));
      if (!ok) return;
    }
    setStepErrors([]);
    setMsg(null);
    save.mutate();
  }

  const securityHint = (() => {
    if (encryption === "tls") return t("admin:mail.wizard.security.hintTls");
    if (encryption === "starttls") return t("admin:mail.wizard.security.hintStarttls");
    return t("admin:mail.wizard.security.hintNone");
  })();

  return (
    <Panel>
      <PanelHeader>
        <h3 className="text-sm font-semibold text-fg">{t("admin:mail.wizard.panelTitle")}</h3>
        <Stepper current={step} completed={completed} />
      </PanelHeader>
      <PanelBody>
        <form onSubmit={submit} className="space-y-4">
          {step === "transport" && (
            <div className="space-y-4">
              <div className="grid grid-cols-1 gap-3 md:grid-cols-2">
                <Field label={t("admin:mail.wizard.fields.host")}>
                  <TextInput
                    type="text"
                    required
                    value={host}
                    onChange={(e: ChangeEvent<HTMLInputElement>) => { setHost(e.target.value); }}
                    placeholder="smtp.example.com"
                    autoFocus
                  />
                </Field>
                <Field label={t("admin:mail.wizard.fields.port")}>
                  <TextInput
                    type="number"
                    required
                    min={1}
                    max={65535}
                    value={port}
                    onChange={(e) => { setPort(Number(e.target.value)); }}
                  />
                </Field>
              </div>

              <fieldset className="space-y-2 rounded-md border border-border p-3 text-sm">
                <legend className="px-1 text-xs uppercase tracking-wide text-fg-muted">
                  {t("admin:mail.wizard.security.legend")}
                </legend>
                <label className="flex items-center gap-2">
                  <input
                    type="radio"
                    name="encryption"
                    value="starttls"
                    checked={encryption === "starttls"}
                    onChange={() => { setEncryption("starttls"); }}
                  />
                  {t("admin:mail.wizard.security.starttls")}
                </label>
                <label className="flex items-center gap-2">
                  <input
                    type="radio"
                    name="encryption"
                    value="tls"
                    checked={encryption === "tls"}
                    onChange={() => { setEncryption("tls"); }}
                  />
                  {t("admin:mail.wizard.security.tls")}
                </label>
                <label className="flex items-center gap-2">
                  <input
                    type="radio"
                    name="encryption"
                    value="none"
                    checked={encryption === "none"}
                    onChange={() => { setEncryption("none"); }}
                  />
                  {t("admin:mail.wizard.security.none")}
                </label>
                <label
                  className={`flex items-center gap-2 ${encryption === "none" ? "opacity-50" : ""}`}
                >
                  <input
                    type="checkbox"
                    checked={insecureSkipVerify}
                    disabled={encryption === "none"}
                    onChange={(e) => { setInsecureSkipVerify(e.target.checked); }}
                  />
                  <span>
                    {t("admin:mail.wizard.security.skipVerify")}{" "}
                    <span className="text-fail">
                      {t("admin:mail.wizard.security.skipVerifyWarn")}
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
                      ? t("admin:mail.wizard.testConn.hintNoHost")
                      : !myEmail
                        ? t("admin:mail.wizard.testConn.hintNoEmail")
                        : t("admin:mail.wizard.testConn.hintReady", { email: myEmail })
                  }
                >
                  <Wifi className="h-3.5 w-3.5" />
                  {testConn.isPending
                    ? t("admin:mail.wizard.testConn.testing")
                    : t("admin:mail.wizard.testConn.button")}
                </Button>
                <span className="text-xs text-fg-subtle">
                  {t("admin:mail.wizard.testConn.note")}
                </span>
              </div>
            </div>
          )}

          {step === "identity" && (
            <div className="space-y-4">
              <div className="grid grid-cols-1 gap-3 md:grid-cols-2">
                <Field
                  label={t("admin:mail.wizard.fields.username")}
                  hint={t("admin:mail.wizard.fields.usernameHint")}
                >
                  <TextInput
                    type="text"
                    value={username}
                    onChange={(e) => { setUsername(e.target.value); }}
                    autoFocus
                  />
                </Field>
                <Field
                  label={
                    initial.has_password
                      ? t("admin:mail.wizard.fields.passwordKeep")
                      : t("admin:mail.wizard.fields.password")
                  }
                >
                  <TextInput
                    type="password"
                    value={password}
                    onChange={(e) => { setPassword(e.target.value); }}
                    placeholder={initial.has_password ? "********" : ""}
                    disabled={clearPassword}
                    className="font-mono"
                  />
                </Field>
                <Field label={t("admin:mail.wizard.fields.fromAddress")}>
                  <TextInput
                    type="email"
                    required
                    value={fromAddress}
                    onChange={(e) => { setFromAddress(e.target.value); }}
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
                    {t("admin:mail.wizard.fields.wipePassword")}
                  </label>
                )}
              </div>

              <div className="rounded-md border border-border bg-panel-2 p-3 text-xs text-fg-muted">
                <div className="mb-1 flex items-center gap-1.5 font-medium text-fg">
                  <Check className="h-3.5 w-3.5 text-ok" /> {t("admin:mail.wizard.review.heading")}
                </div>
                <dl className="grid grid-cols-2 gap-x-4 gap-y-1 font-mono">
                  <dt className="text-fg-subtle">{t("admin:mail.wizard.review.host")}</dt>
                  <dd>{host || "—"}</dd>
                  <dt className="text-fg-subtle">{t("admin:mail.wizard.review.port")}</dt>
                  <dd>{port}</dd>
                  <dt className="text-fg-subtle">{t("admin:mail.wizard.review.security")}</dt>
                  <dd>
                    <StatusPill status={encryption === "none" ? "warn" : "ok"}>
                      {encryption}
                    </StatusPill>
                  </dd>
                  <dt className="text-fg-subtle">{t("admin:mail.wizard.review.skipVerify")}</dt>
                  <dd>
                    {insecureSkipVerify ? t("admin:mail.status.yes") : t("admin:mail.status.no")}
                  </dd>
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
                {t("admin:mail.wizard.buttons.next")} <ChevronRight className="h-3.5 w-3.5" />
              </Button>
            ) : (
              <>
                <Button type="button" onClick={back}>
                  <ChevronLeft className="h-3.5 w-3.5" /> {t("common:actions.back")}
                </Button>
                <Button type="submit" variant="primary" disabled={save.isPending}>
                  {save.isPending
                    ? t("admin:mail.wizard.buttons.saving")
                    : t("admin:mail.wizard.buttons.save")}
                </Button>
              </>
            )}
            {initial.updated_at && (
              <span className="text-xs text-fg-subtle">
                {t("admin:mail.wizard.lastUpdated", {
                  when: new Date(initial.updated_at).toLocaleString(),
                })}
                {initial.updated_by
                  ? t("admin:mail.wizard.lastUpdatedBy", { user: initial.updated_by })
                  : ""}
              </span>
            )}
          </div>
        </form>
      </PanelBody>
    </Panel>
  );
}

function TestCard({
  defaultTo,
  onOutcome,
}: {
  defaultTo: string;
  onOutcome: (o: TestOutcome) => void;
}) {
  const { t } = useT(["admin"]);
  const [to, setTo] = useState(defaultTo);
  const [msg, setMsg] = useState<Msg>(null);
  // Append-only log of recent send attempts (newest first), capped at 5.
  const [log, setLog] = useState<TestOutcome[]>([]);

  function recordOutcome(o: TestOutcome) {
    onOutcome(o);
    setLog((prev) => [o, ...prev].slice(0, 5));
  }

  const send = useMutation({
    mutationFn: () =>
      api<{ ok: boolean; error?: string }>("/v1/admin/mail/test", {
        method: "POST",
        body: JSON.stringify({ to }),
      }),
    onSuccess: (data) => {
      if (data.ok) {
        const text = t("admin:mail.test.dispatched", { to });
        setMsg({ kind: "ok", text });
        recordOutcome({ kind: "ok", text, at: new Date().toISOString(), source: "send-test" });
      } else {
        const text = data.error || t("admin:mail.test.failed");
        setMsg({ kind: "err", text });
        recordOutcome({ kind: "err", text, at: new Date().toISOString(), source: "send-test" });
      }
    },
    onError: (err) => {
      const text = err instanceof ApiError ? err.detail : t("admin:mail.test.failed");
      setMsg({ kind: "err", text });
      recordOutcome({ kind: "err", text, at: new Date().toISOString(), source: "send-test" });
    },
  });

  return (
    <Panel>
      <PanelHeader>
        <h3 className="text-sm font-semibold text-fg">{t("admin:mail.test.title")}</h3>
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
          <Field label={t("admin:mail.test.recipient")}>
            <TextInput
              type="email"
              required
              value={to}
              onChange={(e) => { setTo(e.target.value); }}
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
            {send.isPending ? t("admin:mail.test.sending") : t("admin:mail.test.send")}
          </Button>
        </form>

        {log.length > 0 && (
          <div className="mt-5">
            <h4 className="mb-2 text-[11px] font-semibold uppercase tracking-[0.08em] text-fg-subtle">
              {t("admin:mail.test.recentResults")}
            </h4>
            <ul className="space-y-1.5 text-xs">
              {log.map((entry) => (
                <li
                  key={entry.at}
                  className="flex items-start gap-2 rounded-md border border-border bg-panel-2 px-2.5 py-1.5"
                >
                  <StatusPill status={entry.kind === "ok" ? "ok" : "fail"}>
                    {entry.kind === "ok"
                      ? t("admin:mail.test.outcomeOk")
                      : t("admin:mail.test.outcomeFail")}
                  </StatusPill>
                  <div className="min-w-0 flex-1">
                    <p className="truncate text-fg">{entry.text}</p>
                    <p className="text-fg-subtle">
                      {new Date(entry.at).toLocaleString()}
                    </p>
                  </div>
                </li>
              ))}
            </ul>
          </div>
        )}
      </PanelBody>
    </Panel>
  );
}

// ---- Status tab -----------------------------------------------------------

function StatusCard({
  settings,
  lastOutcome,
}: {
  settings: SmtpSettings;
  lastOutcome: TestOutcome | null;
}) {
  const { t } = useT(["admin"]);
  // Ready = saved settings define a transport. Sending may still fail at the
  // network layer; the badge below reflects last observed test outcome.
  const ready = Boolean(settings.host && settings.from_address);
  const encryption = deriveEncryption(settings.starttls, settings.tls);

  return (
    <Panel>
      <PanelHeader>
        <h3 className="text-sm font-semibold text-fg">{t("admin:mail.status.title")}</h3>
        <div className="flex items-center gap-2">
          {ready ? (
            <StatusPill status="ok">{t("admin:mail.status.ready")}</StatusPill>
          ) : (
            <StatusPill status="warn">{t("admin:mail.status.notConfigured")}</StatusPill>
          )}
          {lastOutcome &&
            (lastOutcome.kind === "ok" ? (
              <StatusPill status="ok">{t("admin:mail.status.lastTestOk")}</StatusPill>
            ) : (
              <StatusPill status="fail">{t("admin:mail.status.lastTestFailed")}</StatusPill>
            ))}
        </div>
      </PanelHeader>
      <PanelBody className="space-y-4">
        <dl className="grid grid-cols-2 gap-x-4 gap-y-1 font-mono text-xs">
          <dt className="text-fg-subtle">{t("admin:mail.status.fields.host")}</dt>
          <dd>{settings.host || "—"}</dd>
          <dt className="text-fg-subtle">{t("admin:mail.status.fields.port")}</dt>
          <dd>{settings.port || "—"}</dd>
          <dt className="text-fg-subtle">{t("admin:mail.status.fields.security")}</dt>
          <dd>
            <StatusPill status={encryption === "none" ? "warn" : "ok"}>{encryption}</StatusPill>
          </dd>
          <dt className="text-fg-subtle">{t("admin:mail.status.fields.from")}</dt>
          <dd>{settings.from_address || "—"}</dd>
          <dt className="text-fg-subtle">{t("admin:mail.status.fields.password")}</dt>
          <dd>{settings.has_password ? t("admin:mail.status.stored") : "—"}</dd>
          <dt className="text-fg-subtle">{t("admin:mail.status.fields.skipVerify")}</dt>
          <dd>
            {settings.insecure_skip_verify
              ? t("admin:mail.status.yes")
              : t("admin:mail.status.no")}
          </dd>
          {settings.updated_at && (
            <>
              <dt className="text-fg-subtle">{t("admin:mail.status.fields.updated")}</dt>
              <dd>
                {new Date(settings.updated_at).toLocaleString()}
                {settings.updated_by
                  ? t("admin:mail.wizard.lastUpdatedBy", { user: settings.updated_by })
                  : ""}
              </dd>
            </>
          )}
        </dl>

        {lastOutcome && (
          <div className="rounded-md border border-border bg-panel-2 p-3 text-xs">
            <div className="mb-1 flex items-center gap-1.5 font-medium text-fg">
              <Activity className="h-3.5 w-3.5 text-accent" />
              {t("admin:mail.status.lastTestHeading", {
                source:
                  lastOutcome.source === "transport-probe"
                    ? t("admin:mail.status.sourceTransport")
                    : t("admin:mail.status.sourceSendTest"),
              })}
            </div>
            <p className={lastOutcome.kind === "ok" ? "text-ok" : "text-fail"}>
              {lastOutcome.text}
            </p>
            <p className="mt-0.5 text-fg-subtle">
              {new Date(lastOutcome.at).toLocaleString()}
            </p>
          </div>
        )}

        <div className="rounded-md border border-border bg-panel-2 p-3 text-xs text-fg-muted">
          <p className="mb-1 font-medium text-fg">{t("admin:mail.status.troubleshooting.heading")}</p>
          <ul className="list-disc space-y-0.5 pl-4">
            <li>
              <span className="text-fg">{t("admin:mail.status.troubleshooting.connectionLabel")}</span>
              {t("admin:mail.status.troubleshooting.connection")}
            </li>
            <li>
              <span className="text-fg">{t("admin:mail.status.troubleshooting.tlsLabel")}</span>
              {t("admin:mail.status.troubleshooting.tlsLead")}
              <em>{t("admin:mail.status.troubleshooting.tlsEm")}</em>
              {t("admin:mail.status.troubleshooting.tlsTail")}
            </li>
            <li>
              <span className="text-fg">{t("admin:mail.status.troubleshooting.authLabel")}</span>
              {t("admin:mail.status.troubleshooting.auth")}
            </li>
            <li>
              <span className="text-fg">{t("admin:mail.status.troubleshooting.senderLabel")}</span>
              {t("admin:mail.status.troubleshooting.sender")}
            </li>
            <li>
              {t("admin:mail.status.troubleshooting.probeLead")}
              <span className="font-medium text-fg">
                {t("admin:mail.status.troubleshooting.probeTab")}
              </span>
              {t("admin:mail.status.troubleshooting.probeTail")}
            </li>
          </ul>
        </div>
      </PanelBody>
    </Panel>
  );
}
