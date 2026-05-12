import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { ShieldAlert } from "lucide-react";
import { FormEvent, useEffect, useState } from "react";

import {
  Button,
  ErrorBox,
  Panel,
  PanelBody,
  PanelHeader,
  Skeleton,
  SuccessBox,
  TextInput,
} from "../components/ui";
import { api, ApiError } from "../lib/api";
import { ForceMode, PasswordPolicy, RevokeAllSessionsResponse, SecurityPolicy } from "../lib/types";

// TODO(theme): this page still uses raw `zinc-*` Tailwind classes which
// don't follow the dark/light palette. Migrate to semantic tokens
// (text-fg-muted, bg-panel, border-border, …) in a follow-up.

export function AdminSecurity() {
  const qc = useQueryClient();
  const policy = useQuery({
    queryKey: ["password-policy"],
    queryFn: () => api<PasswordPolicy>("/v1/admin/security/password-policy"),
  });

  const [draft, setDraft] = useState<PasswordPolicy | null>(null);
  useEffect(() => {
    if (policy.data) setDraft(policy.data);
  }, [policy.data]);

  const [msg, setMsg] = useState<{ kind: "ok" | "err"; text: string } | null>(null);

  const save = useMutation({
    mutationFn: (next: PasswordPolicy) =>
      api<PasswordPolicy>("/v1/admin/security/password-policy", {
        method: "PUT",
        body: JSON.stringify(next),
      }),
    onSuccess: () => {
      setMsg({ kind: "ok", text: "Policy updated." });
      qc.invalidateQueries({ queryKey: ["password-policy"] });
    },
    onError: (err) => setMsg({ kind: "err", text: err instanceof ApiError ? err.detail : "failed" }),
  });

  // Force-Modus & Session-Limits ------------------------------------------------
  const sec = useQuery({
    queryKey: ["security-policy"],
    queryFn: () => api<SecurityPolicy>("/v1/admin/security/policy"),
  });

  const [secDraft, setSecDraft] = useState<SecurityPolicy | null>(null);
  useEffect(() => {
    if (sec.data) setSecDraft(sec.data);
  }, [sec.data]);

  const [secMsg, setSecMsg] = useState<{ kind: "ok" | "err"; text: string } | null>(null);

  const saveSec = useMutation({
    mutationFn: (next: SecurityPolicy) =>
      api<{ ok: boolean }>("/v1/admin/security/policy", {
        method: "PUT",
        body: JSON.stringify(next),
      }),
    onSuccess: () => {
      setSecMsg({ kind: "ok", text: "Force-Modus aktualisiert." });
      qc.invalidateQueries({ queryKey: ["security-policy"] });
    },
    onError: (err) =>
      setSecMsg({ kind: "err", text: err instanceof ApiError ? err.detail : "failed" }),
  });

  const revokeAll = useMutation({
    mutationFn: () =>
      api<RevokeAllSessionsResponse>("/v1/admin/security/revoke-all-sessions", {
        method: "POST",
      }),
    onSuccess: (data) =>
      setSecMsg({
        kind: "ok",
        text: `Revoked ${data.revoked} sessions (your own session was preserved)`,
      }),
    onError: (err) =>
      setSecMsg({ kind: "err", text: err instanceof ApiError ? err.detail : "failed" }),
  });

  if (policy.isLoading || !draft || sec.isLoading || !secDraft)
    return (
      <div className="mx-auto max-w-3xl space-y-4 p-6">
        <Skeleton className="h-7 w-56" />
        <Skeleton className="h-64" />
        <Skeleton className="h-64" />
      </div>
    );

  const set =
    <K extends keyof PasswordPolicy>(key: K) =>
    (value: PasswordPolicy[K]) =>
      setDraft({ ...draft, [key]: value });

  function submit(e: FormEvent) {
    e.preventDefault();
    save.mutate(draft!);
  }

  const VALID_FORCE_MODES: ForceMode[] = ["off", "2fa_any", "passkey_required"];
  const VALID_GRACE_DAYS = [0, 1, 7, 30];

  const setSec =
    <K extends keyof SecurityPolicy>(key: K) =>
    (value: SecurityPolicy[K]) =>
      setSecDraft({ ...secDraft!, [key]: value });

  function submitSec(e: FormEvent) {
    e.preventDefault();
    setSecMsg(null);
    const d = secDraft!;
    if (!VALID_FORCE_MODES.includes(d.force_mode)) {
      setSecMsg({ kind: "err", text: "Ungültiger Force-Modus." });
      return;
    }
    if (!VALID_GRACE_DAYS.includes(d.grace_days)) {
      setSecMsg({ kind: "err", text: "Grace-Period muss 0, 1, 7 oder 30 Tage sein." });
      return;
    }
    if (
      !Number.isFinite(d.max_session_hours) ||
      d.max_session_hours < 1 ||
      d.max_session_hours > 720
    ) {
      setSecMsg({ kind: "err", text: "Max-Session muss zwischen 1 und 720 Stunden liegen." });
      return;
    }
    if (
      !Number.isFinite(d.idle_timeout_minutes) ||
      d.idle_timeout_minutes < 0 ||
      d.idle_timeout_minutes > 10080
    ) {
      setSecMsg({ kind: "err", text: "Idle-Timeout muss zwischen 0 und 10080 Minuten liegen." });
      return;
    }
    saveSec.mutate(d);
  }

  function onRevokeAll() {
    if (
      !window.confirm(
        "Alle aktiven Sessions (außer deiner eigenen) werden invalidiert. Fortfahren?",
      )
    ) {
      return;
    }
    setSecMsg(null);
    revokeAll.mutate();
  }

  const escalatingToPasskey =
    sec.data?.force_mode === "off" && secDraft.force_mode === "passkey_required";

  const selectCls =
    "w-full rounded-md border border-border bg-panel px-3 py-2 text-sm text-fg focus:border-accent focus:outline-none focus:ring-2 focus:ring-accent/30";

  return (
    <div className="mx-auto max-w-3xl space-y-6 p-6">
      <header>
        <h2 className="text-lg font-semibold">Security</h2>
        <p className="text-sm text-fg-muted">Password requirements applied to all new and changed passwords.</p>
      </header>

      <Panel>
        <PanelHeader>
          <h3 className="text-sm font-semibold text-fg">Password policy</h3>
        </PanelHeader>
        <PanelBody>
          <form onSubmit={submit} className="space-y-4">
            <label className="block">
              <span className="mb-1 block text-xs font-medium text-fg-muted">Minimum length</span>
              <TextInput
                type="number"
                min={4}
                max={128}
                value={draft.min_length}
                onChange={(e) => set("min_length")(parseInt(e.target.value || "0", 10))}
                className="w-32"
              />
            </label>

            <fieldset className="grid grid-cols-2 gap-2 text-sm text-fg">
              <Toggle label="Uppercase letter" value={draft.require_upper} onChange={set("require_upper")} />
              <Toggle label="Lowercase letter" value={draft.require_lower} onChange={set("require_lower")} />
              <Toggle label="Digit" value={draft.require_digit} onChange={set("require_digit")} />
              <Toggle label="Symbol" value={draft.require_symbol} onChange={set("require_symbol")} />
            </fieldset>

            <label className="block">
              <span className="mb-1 block text-xs font-medium text-fg-muted">
                Max age (days, 0 = no expiry)
              </span>
              <TextInput
                type="number"
                min={0}
                value={draft.max_age_days}
                onChange={(e) => set("max_age_days")(parseInt(e.target.value || "0", 10))}
                className="w-32"
              />
            </label>

            <Button type="submit" variant="primary" disabled={save.isPending}>
              {save.isPending ? "Saving…" : "Save policy"}
            </Button>
            {msg &&
              (msg.kind === "ok" ? (
                <SuccessBox>{msg.text}</SuccessBox>
              ) : (
                <ErrorBox>{msg.text}</ErrorBox>
              ))}
          </form>
        </PanelBody>
      </Panel>

      <Panel>
        <PanelHeader>
          <h3 className="text-sm font-semibold text-fg">Force-Modus &amp; Session-Limits</h3>
        </PanelHeader>
        <PanelBody>
          <form onSubmit={submitSec} className="space-y-4">
            <label className="block">
              <span className="mb-1 block text-xs font-medium text-fg-muted">Force-Modus</span>
              <select
                value={secDraft.force_mode}
                onChange={(e) => setSec("force_mode")(e.target.value as ForceMode)}
                className={selectCls}
              >
                <option value="off">Aus (kein Zwang)</option>
                <option value="2fa_any">Passkey ODER TOTP für alle</option>
                <option value="passkey_required">Passkey verpflichtend für alle</option>
              </select>
            </label>

            <label className="block">
              <span className="mb-1 block text-xs font-medium text-fg-muted">
                Grace-Period (Tage)
              </span>
              <select
                value={secDraft.grace_days}
                onChange={(e) => setSec("grace_days")(parseInt(e.target.value, 10))}
                className={selectCls}
              >
                <option value={0}>0 — sofort</option>
                <option value={1}>1 Tag</option>
                <option value={7}>7 Tage</option>
                <option value={30}>30 Tage</option>
              </select>
            </label>

            <label className="block">
              <span className="mb-1 block text-xs font-medium text-fg-muted">
                Max Session (Stunden, 1–720)
              </span>
              <TextInput
                type="number"
                min={1}
                max={720}
                value={secDraft.max_session_hours}
                onChange={(e) =>
                  setSec("max_session_hours")(parseInt(e.target.value || "0", 10))
                }
                className="w-32"
              />
            </label>

            <label className="block">
              <span className="mb-1 block text-xs font-medium text-fg-muted">
                Idle-Timeout (Minuten, 0–10080)
              </span>
              <TextInput
                type="number"
                min={0}
                max={10080}
                value={secDraft.idle_timeout_minutes}
                onChange={(e) =>
                  setSec("idle_timeout_minutes")(parseInt(e.target.value || "0", 10))
                }
                className="w-32"
              />
              <span className="mt-1 block text-xs text-fg-muted">0 = aus</span>
            </label>

            {escalatingToPasskey && (
              <div className="flex items-start gap-2 rounded-md border border-yellow-500/40 bg-yellow-500/10 p-3 text-sm text-yellow-200">
                <ShieldAlert className="mt-0.5 h-4 w-4 shrink-0" aria-hidden />
                <span>
                  Achtung: alle Nutzer ohne Passkey werden nach Grace-Period gesperrt.
                  Sicherstellen, dass mindestens du selbst einen Passkey eingerichtet hast.
                </span>
              </div>
            )}

            <div className="flex flex-wrap items-center gap-3">
              <Button type="submit" variant="primary" disabled={saveSec.isPending}>
                {saveSec.isPending ? "Speichern…" : "Speichern"}
              </Button>
              <Button
                type="button"
                variant="danger"
                onClick={onRevokeAll}
                disabled={revokeAll.isPending}
              >
                {revokeAll.isPending ? "Revoking…" : "Alle Sessions revoken"}
              </Button>
            </div>

            {secMsg &&
              (secMsg.kind === "ok" ? (
                <SuccessBox>{secMsg.text}</SuccessBox>
              ) : (
                <ErrorBox>{secMsg.text}</ErrorBox>
              ))}
          </form>
        </PanelBody>
      </Panel>
    </div>
  );
}

function Toggle(props: { label: string; value: boolean; onChange: (v: boolean) => void }) {
  return (
    <label className="flex items-center gap-2">
      <input
        type="checkbox"
        checked={props.value}
        onChange={(e) => props.onChange(e.target.checked)}
      />
      {props.label}
    </label>
  );
}
