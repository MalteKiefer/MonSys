import { useMutation, useQuery } from "@tanstack/react-query";
import { Bell, Hash, Mail, MessageCircle, MessageSquare } from "lucide-react";
import type { SyntheticEvent} from "react";
import { useState } from "react";

import {
  Button,
  ErrorBox,
  Field,
  Panel,
  PanelBody,
  PanelHeader,
  TextInput,
} from "../../components/ui";
import {
  FormSection,
  Toggle,
  TypeCardGrid,
} from "../../components/notifications/FormParts";
import { useT } from "../../i18n/useT";
import { api, ApiError } from "../../lib/api";
import { useAuth } from "../../lib/auth";
import type {
  AuthConfig,
  ChannelType,
  NotificationChannel,
  NotificationChannelInput,
} from "../../lib/types";

// ---- Channel form ---------------------------------------------------------
//
// Extracted verbatim from the original AdminNotifications monolith. Same
// props as before; consumed by `ChannelsPage`.

// Channel-form schema by type. Sensitive fields shown as password inputs.
// type=email is special-cased in ChannelForm: it has no per-channel config,
// only a top-level recipient_email field. Transport (host/auth/from) lives
// in the admin-managed SMTP settings.
// Labels come from the i18n bundle — we keep the field-key list static here
// and translate inside the render closure.
interface FieldDef { key: string; labelKey: string; type?: string; placeholder?: string; help?: string }
const CHANNEL_FIELDS: Record<Exclude<ChannelType, "email">, FieldDef[]> = {
  slack: [
    { key: "webhook_url", labelKey: "webhook_url", type: "password", placeholder: "https://hooks.slack.com/…" },
  ],
  mattermost: [
    { key: "webhook_url", labelKey: "webhook_url", type: "password" },
    { key: "username", labelKey: "username_optional", placeholder: "MonSys" },
  ],
  discord: [
    { key: "webhook_url", labelKey: "discord_webhook_url", type: "password", placeholder: "https://discord.com/api/webhooks/…" },
    { key: "username", labelKey: "username_optional", placeholder: "MonSys" },
  ],
  ntfy: [
    { key: "server_url", labelKey: "server_url", placeholder: "https://ntfy.sh" },
    { key: "topic", labelKey: "topic" },
    { key: "auth_token", labelKey: "auth_token_optional", type: "password" },
    { key: "username", labelKey: "basic_user_optional" },
    { key: "password", labelKey: "basic_pass_optional", type: "password" },
  ],
};

// Card data for the channel-type picker. Order matches the previous select.
// Labels and descriptions come from i18n; we just declare value+icon here.
const CHANNEL_TYPE_CARDS: {
  value: ChannelType;
  icon: typeof Mail;
}[] = [
  { value: "email", icon: Mail },
  { value: "slack", icon: MessageSquare },
  { value: "mattermost", icon: MessageCircle },
  { value: "discord", icon: MessageCircle },
  { value: "ntfy", icon: Bell },
];

function parseConfig(_type: ChannelType, cfg: Record<string, string>): Record<string, unknown> {
  const out: Record<string, unknown> = {};
  for (const [k, v] of Object.entries(cfg)) {
    if (v === "") continue;
    if (k === "port") {
      const n = Number(v);
      out[k] = Number.isFinite(n) ? n : v;
    } else if (k === "starttls" || k === "tls" || k === "insecure_skip_verify") {
      out[k] = v.toLowerCase() === "true";
    } else {
      out[k] = v;
    }
  }
  return out;
}

export function ChannelForm({
  initial,
  isAdmin: _isAdmin,
  onCancel,
  onSaved,
}: {
  initial: NotificationChannel | null;
  isAdmin: boolean;
  onCancel: () => void;
  onSaved: () => void;
}) {
  const { t } = useT(["notifications", "common"]);
  const myEmail = useAuth((s) => s.user?.email ?? "");
  // Server-wide readiness flags: lets us warn a non-admin that creating an
  // email channel is pointless until SMTP is configured. Falls back silently
  // (smtp_configured stays falsy → warning shows) if the call fails.
  const authConfig = useQuery({
    queryKey: ["auth-config"],
    queryFn: () => api<AuthConfig>("/v1/auth/config"),
    staleTime: 30_000,
  });
  const [type, setType] = useState<ChannelType>(initial?.type ?? "email");
  const [name, setName] = useState(initial?.name ?? "");
  const [enabled, setEnabled] = useState(initial?.enabled ?? true);
  const [recipientEmail, setRecipientEmail] = useState<string>(
    initial?.recipient_email ?? (initial ? "" : myEmail),
  );
  const [config, setConfig] = useState<Record<string, string>>(() => {
    const out: Record<string, string> = {};
    if (initial?.config) {
      for (const [k, v] of Object.entries(initial.config)) {
        out[k] = v == null ? "" : String(v);
      }
    }
    return out;
  });
  const [error, setError] = useState<string | null>(null);

  const save = useMutation({
    mutationFn: () => {
      const body: NotificationChannelInput =
        type === "email"
          ? { type, name, enabled, recipient_email: recipientEmail || myEmail, config: {} }
          : { type, name, enabled, config: parseConfig(type, config) };
      if (initial) {
        return api(`/v1/notifications/channels/${initial.id}`, {
          method: "PUT",
          body: JSON.stringify(body),
        });
      }
      return api("/v1/notifications/channels", {
        method: "POST",
        body: JSON.stringify(body),
      });
    },
    onSuccess: onSaved,
    onError: (err) => { setError(err instanceof ApiError ? err.detail : t("notifications:channels.form.error_generic")); },
  });

  function submit(e: SyntheticEvent) {
    e.preventDefault();
    setError(null);
    save.mutate();
  }

  const fields = type === "email" ? [] : CHANNEL_FIELDS[type];

  // Build the translated card options each render — TypeCardGrid wants
  // `{ value, label, description, icon }`.
  const typeCardOptions = CHANNEL_TYPE_CARDS.map((c) => ({
    value: c.value,
    icon: c.icon,
    label: t(`notifications:channels.form.types.${c.value}.label` as const),
    description: t(`notifications:channels.form.types.${c.value}.description` as const),
  }));

  return (
    <Panel>
      <PanelHeader>
        <h3 className="text-sm font-semibold">
          {initial
            ? t("notifications:channels.form.title_edit", { name: initial.name })
            : t("notifications:channels.form.title_new")}
        </h3>
      </PanelHeader>
      <PanelBody>
        <form onSubmit={submit} className="space-y-5">
          <FormSection
            label={t("notifications:channels.form.type_section")}
            hint={initial ? t("notifications:channels.form.type_locked_hint") : undefined}
          >
            <TypeCardGrid
              value={type}
              disabled={!!initial}
              onChange={(next) => {
                setType(next);
                setConfig({});
                if (next === "email") setRecipientEmail(myEmail);
              }}
              options={typeCardOptions}
            />
          </FormSection>

          <FormSection label={t("notifications:channels.form.identification_section")} divider={false}>
            <Field label={t("notifications:channels.form.name_label")}>
              <TextInput
                required
                value={name}
                onChange={(e) => { setName(e.target.value); }}
                placeholder={t("notifications:channels.form.name_placeholder")}
              />
            </Field>

            {type === "email" ? (
              <>
                {authConfig.data?.smtp_configured === false && (
                  <p className="rounded-md border border-warn/30 bg-warn/10 px-3 py-2 text-xs text-warn">
                    {t("notifications:channels.form.smtp_warning")}
                  </p>
                )}
                <Field
                  label={t("notifications:channels.form.recipient_email_label")}
                  hint={t("notifications:channels.form.recipient_email_hint")}
                >
                  <TextInput
                    type="email"
                    required
                    value={recipientEmail}
                    onChange={(e) => { setRecipientEmail(e.target.value); }}
                    placeholder={myEmail}
                  />
                </Field>
              </>
            ) : (
              <div className="rounded-md border border-border bg-panel-2/40 p-3">
                <div className="mb-3 flex items-center gap-2 text-[11px] uppercase tracking-[0.08em] text-fg-subtle">
                  <Hash className="h-3 w-3" />
                  {type} {t("notifications:channels.form.config_section_suffix")}
                </div>
                <div className="grid grid-cols-1 gap-3 md:grid-cols-2">
                  {fields.map((f) => (
                    <Field
                      key={f.key}
                      label={t(`notifications:channels.form.fields.${f.labelKey}` as const)}
                      hint={f.help}
                    >
                      <TextInput
                        type={f.type || "text"}
                        placeholder={f.placeholder}
                        value={config[f.key] ?? ""}
                        onChange={(e) => { setConfig({ ...config, [f.key]: e.target.value }); }}
                        className={f.type === "password" ? "font-mono" : ""}
                      />
                    </Field>
                  ))}
                </div>
              </div>
            )}

            <Toggle
              checked={enabled}
              onChange={setEnabled}
              label={t("notifications:channels.form.enabled_label")}
              hint={t("notifications:channels.form.enabled_hint")}
            />
          </FormSection>

          {error && <ErrorBox>{error}</ErrorBox>}

          <div className="flex items-center gap-2 pt-1">
            <Button variant="primary" type="submit" disabled={save.isPending}>
              {save.isPending
                ? t("notifications:channels.form.saving")
                : initial
                  ? t("notifications:channels.form.save")
                  : t("notifications:channels.form.create")}
            </Button>
            <Button type="button" onClick={onCancel}>
              {t("notifications:channels.form.cancel")}
            </Button>
          </div>
        </form>
      </PanelBody>
    </Panel>
  );
}
