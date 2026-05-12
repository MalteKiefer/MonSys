import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { Activity, Lock, Shield, ShieldAlert } from "lucide-react";
import { FormEvent, useEffect, useState } from "react";
import { useSearchParams } from "react-router-dom";

import {
  Button,
  ErrorBox,
  Panel,
  PanelBody,
  PanelHeader,
  Skeleton,
  SuccessBox,
  Tabs,
  type TabItem,
  TextInput,
} from "../components/ui";
import { useT } from "../i18n/useT";
import { api, ApiError } from "../lib/api";
import { ForceMode, PasswordPolicy, RevokeAllSessionsResponse, SecurityPolicy } from "../lib/types";

type TabKey = "password" | "auth" | "sessions";

function isTabKey(v: string | null): v is TabKey {
  return v === "password" || v === "auth" || v === "sessions";
}

export function AdminSecurity() {
  const { t } = useT(["admin", "common"]);
  const qc = useQueryClient();

  const TAB_ITEMS: ReadonlyArray<TabItem<TabKey>> = [
    { key: "password", label: t("security.tabs.password"), icon: Lock },
    { key: "auth", label: t("security.tabs.auth"), icon: Shield },
    { key: "sessions", label: t("security.tabs.sessions"), icon: Activity },
  ];

  // Deep-link via ?tab=… — fall back to "password".
  const [searchParams, setSearchParams] = useSearchParams();
  const initialTab = searchParams.get("tab");
  const [activeTab, setActiveTab] = useState<TabKey>(isTabKey(initialTab) ? initialTab : "password");

  function onTabChange(next: TabKey) {
    setActiveTab(next);
    const params = new URLSearchParams(searchParams);
    params.set("tab", next);
    setSearchParams(params, { replace: true });
  }

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
      setMsg({ kind: "ok", text: t("security.password.saved") });
      qc.invalidateQueries({ queryKey: ["password-policy"] });
    },
    onError: (err) =>
      setMsg({
        kind: "err",
        text: err instanceof ApiError ? err.detail : t("security.password.generic_failure"),
      }),
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
      setSecMsg({ kind: "ok", text: t("security.auth.saved") });
      qc.invalidateQueries({ queryKey: ["security-policy"] });
    },
    onError: (err) =>
      setSecMsg({
        kind: "err",
        text: err instanceof ApiError ? err.detail : t("security.auth.generic_failure"),
      }),
  });

  const revokeAll = useMutation({
    mutationFn: () =>
      api<RevokeAllSessionsResponse>("/v1/admin/security/revoke-all-sessions", {
        method: "POST",
      }),
    onSuccess: (data) =>
      setSecMsg({
        kind: "ok",
        text: t("security.sessions.revoked_toast", { count: data.revoked }),
      }),
    onError: (err) =>
      setSecMsg({
        kind: "err",
        text: err instanceof ApiError ? err.detail : t("security.auth.generic_failure"),
      }),
  });

  if (policy.isLoading || !draft || sec.isLoading || !secDraft)
    return (
      <div className="mx-auto max-w-3xl space-y-4 p-6">
        <Skeleton className="h-7 w-56" />
        <Skeleton className="h-10 w-full" />
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
      setSecMsg({ kind: "err", text: t("security.auth.invalid_force_mode") });
      return;
    }
    if (!VALID_GRACE_DAYS.includes(d.grace_days)) {
      setSecMsg({ kind: "err", text: t("security.auth.invalid_grace") });
      return;
    }
    if (
      !Number.isFinite(d.max_session_hours) ||
      d.max_session_hours < 1 ||
      d.max_session_hours > 720
    ) {
      setSecMsg({ kind: "err", text: t("security.auth.invalid_max_session") });
      return;
    }
    if (
      !Number.isFinite(d.idle_timeout_minutes) ||
      d.idle_timeout_minutes < 0 ||
      d.idle_timeout_minutes > 10080
    ) {
      setSecMsg({ kind: "err", text: t("security.auth.invalid_idle_timeout") });
      return;
    }
    saveSec.mutate(d);
  }

  function onRevokeAll() {
    if (!window.confirm(t("security.sessions.revoke_confirm"))) {
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
        <h2 className="text-lg font-semibold">{t("security.page.title")}</h2>
        <p className="text-sm text-fg-muted">{t("security.page.subtitle")}</p>
      </header>

      <Tabs items={TAB_ITEMS} value={activeTab} onChange={onTabChange} />

      {activeTab === "password" && (
        <div
          role="tabpanel"
          id="panel-password"
          aria-labelledby="tab-password"
        >
          <Panel>
            <PanelHeader>
              <h3 className="text-sm font-semibold text-fg">{t("security.password.panel_title")}</h3>
            </PanelHeader>
            <PanelBody>
              <form onSubmit={submit} className="space-y-4">
                <label className="block">
                  <span className="mb-1 block text-xs font-medium text-fg-muted">
                    {t("security.password.min_length")}
                  </span>
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
                  <Toggle
                    label={t("security.password.require_upper")}
                    value={draft.require_upper}
                    onChange={set("require_upper")}
                  />
                  <Toggle
                    label={t("security.password.require_lower")}
                    value={draft.require_lower}
                    onChange={set("require_lower")}
                  />
                  <Toggle
                    label={t("security.password.require_digit")}
                    value={draft.require_digit}
                    onChange={set("require_digit")}
                  />
                  <Toggle
                    label={t("security.password.require_symbol")}
                    value={draft.require_symbol}
                    onChange={set("require_symbol")}
                  />
                </fieldset>

                <label className="block">
                  <span className="mb-1 block text-xs font-medium text-fg-muted">
                    {t("security.password.max_age")}
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
                  {save.isPending
                    ? t("security.password.submitting")
                    : t("security.password.submit")}
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
        </div>
      )}

      {activeTab === "auth" && (
        <div
          role="tabpanel"
          id="panel-auth"
          aria-labelledby="tab-auth"
        >
          <Panel>
            <PanelHeader>
              <h3 className="text-sm font-semibold text-fg">{t("security.auth.panel_title")}</h3>
            </PanelHeader>
            <PanelBody>
              <form onSubmit={submitSec} className="space-y-4">
                <label className="block">
                  <span className="mb-1 block text-xs font-medium text-fg-muted">
                    {t("security.auth.force_mode_label")}
                  </span>
                  <select
                    value={secDraft.force_mode}
                    onChange={(e) => setSec("force_mode")(e.target.value as ForceMode)}
                    className={selectCls}
                  >
                    <option value="off">{t("security.auth.force_off")}</option>
                    <option value="2fa_any">{t("security.auth.force_2fa_any")}</option>
                    <option value="passkey_required">
                      {t("security.auth.force_passkey_required")}
                    </option>
                  </select>
                </label>

                <label className="block">
                  <span className="mb-1 block text-xs font-medium text-fg-muted">
                    {t("security.auth.grace_label")}
                  </span>
                  <select
                    value={secDraft.grace_days}
                    onChange={(e) => setSec("grace_days")(parseInt(e.target.value, 10))}
                    className={selectCls}
                  >
                    <option value={0}>{t("security.auth.grace_0")}</option>
                    <option value={1}>{t("security.auth.grace_1")}</option>
                    <option value={7}>{t("security.auth.grace_7")}</option>
                    <option value={30}>{t("security.auth.grace_30")}</option>
                  </select>
                </label>

                <label className="block">
                  <span className="mb-1 block text-xs font-medium text-fg-muted">
                    {t("security.auth.max_session_label")}
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
                    {t("security.auth.idle_timeout_label")}
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
                  <span className="mt-1 block text-xs text-fg-muted">
                    {t("security.auth.idle_timeout_hint")}
                  </span>
                </label>

                {escalatingToPasskey && (
                  <div className="flex items-start gap-2 rounded-md border border-warn/40 bg-warn/10 p-3 text-sm text-warn">
                    <ShieldAlert className="mt-0.5 h-4 w-4 shrink-0" aria-hidden />
                    <span>{t("security.auth.escalation_warning")}</span>
                  </div>
                )}

                <div className="flex flex-wrap items-center gap-3">
                  <Button type="submit" variant="primary" disabled={saveSec.isPending}>
                    {saveSec.isPending
                      ? t("security.auth.submitting")
                      : t("security.auth.submit")}
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
      )}

      {activeTab === "sessions" && (
        <div
          role="tabpanel"
          id="panel-sessions"
          aria-labelledby="tab-sessions"
        >
          <Panel>
            <PanelHeader>
              <h3 className="text-sm font-semibold text-fg">
                {t("security.sessions.panel_title")}
              </h3>
            </PanelHeader>
            <PanelBody>
              <div className="space-y-4">
                <p className="text-sm text-fg-muted">{t("security.sessions.description")}</p>

                <div className="flex items-start gap-2 rounded-md border border-fail/30 bg-fail/10 p-3 text-sm text-fail">
                  <ShieldAlert className="mt-0.5 h-4 w-4 shrink-0" aria-hidden />
                  <span>{t("security.sessions.danger")}</span>
                </div>

                <div>
                  <Button
                    type="button"
                    variant="danger"
                    onClick={onRevokeAll}
                    disabled={revokeAll.isPending}
                  >
                    {revokeAll.isPending
                      ? t("security.sessions.revoking")
                      : t("security.sessions.revoke_button")}
                  </Button>
                </div>

                {secMsg &&
                  (secMsg.kind === "ok" ? (
                    <SuccessBox>{secMsg.text}</SuccessBox>
                  ) : (
                    <ErrorBox>{secMsg.text}</ErrorBox>
                  ))}
              </div>
            </PanelBody>
          </Panel>
        </div>
      )}
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
