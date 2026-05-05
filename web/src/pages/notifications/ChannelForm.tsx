import { useMutation, useQuery } from "@tanstack/react-query";
import { Bell, Hash, Mail, MessageCircle, MessageSquare } from "lucide-react";
import { FormEvent, useState } from "react";

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
import { api, ApiError } from "../../lib/api";
import { useAuth } from "../../lib/auth";
import {
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
const CHANNEL_FIELDS: Record<Exclude<ChannelType, "email">, Array<{ key: string; label: string; type?: string; placeholder?: string; help?: string }>> = {
  slack: [
    { key: "webhook_url", label: "Incoming webhook URL", type: "password", placeholder: "https://hooks.slack.com/…" },
  ],
  mattermost: [
    { key: "webhook_url", label: "Incoming webhook URL", type: "password" },
    { key: "username", label: "Display username (optional)", placeholder: "MonSys" },
  ],
  discord: [
    { key: "webhook_url", label: "Webhook URL", type: "password", placeholder: "https://discord.com/api/webhooks/…" },
    { key: "username", label: "Display username (optional)", placeholder: "MonSys" },
  ],
  ntfy: [
    { key: "server_url", label: "Server URL", placeholder: "https://ntfy.sh" },
    { key: "topic", label: "Topic" },
    { key: "auth_token", label: "Bearer token (optional)", type: "password" },
    { key: "username", label: "Basic auth user (optional)" },
    { key: "password", label: "Basic auth password (optional)", type: "password" },
  ],
};

// Card data for the channel-type picker. Order matches the previous select.
const CHANNEL_TYPE_CARDS: Array<{
  value: ChannelType;
  label: string;
  description: string;
  icon: typeof Mail;
}> = [
  { value: "email", label: "Email", description: "Send a templated email", icon: Mail },
  { value: "slack", label: "Slack", description: "Post to a Slack channel via webhook", icon: MessageSquare },
  { value: "mattermost", label: "Mattermost", description: "Post to a Mattermost channel via webhook", icon: MessageCircle },
  { value: "discord", label: "Discord", description: "Post to a Discord channel via webhook", icon: MessageCircle },
  { value: "ntfy", label: "ntfy", description: "Push to an ntfy topic", icon: Bell },
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
    onError: (err) => setError(err instanceof ApiError ? err.detail : "failed"),
  });

  function submit(e: FormEvent) {
    e.preventDefault();
    setError(null);
    save.mutate();
  }

  const fields = type === "email" ? [] : CHANNEL_FIELDS[type];

  return (
    <Panel>
      <PanelHeader>
        <h3 className="text-sm font-semibold">{initial ? `Edit ${initial.name}` : "New channel"}</h3>
      </PanelHeader>
      <PanelBody>
        <form onSubmit={submit} className="space-y-5">
          <FormSection
            label="Channel type"
            hint={initial ? "Type cannot be changed after creation." : undefined}
          >
            <TypeCardGrid
              value={type}
              disabled={!!initial}
              onChange={(next) => {
                setType(next);
                setConfig({});
                if (next === "email") setRecipientEmail(myEmail);
              }}
              options={CHANNEL_TYPE_CARDS}
            />
          </FormSection>

          <FormSection label="Identification" divider={false}>
            <Field label="Name">
              <TextInput
                required
                value={name}
                onChange={(e) => setName(e.target.value)}
                placeholder="e.g. ops-oncall"
              />
            </Field>

            {type === "email" ? (
              <>
                {authConfig.data && authConfig.data.smtp_configured === false && (
                  <p className="rounded-md border border-warn/30 bg-warn/10 px-3 py-2 text-xs text-warn">
                    Outbound mail isn't configured yet — ask an admin to set up SMTP under Admin → Mail before this channel can deliver.
                  </p>
                )}
                <Field
                  label="Recipient email"
                  hint="Defaults to your account email. Outbound transport (SMTP host, auth, from address) is configured by an admin under Admin → Mail (SMTP)."
                >
                  <TextInput
                    type="email"
                    required
                    value={recipientEmail}
                    onChange={(e) => setRecipientEmail(e.target.value)}
                    placeholder={myEmail}
                  />
                </Field>
              </>
            ) : (
              <div className="rounded-md border border-border bg-panel-2/40 p-3">
                <div className="mb-3 flex items-center gap-2 text-[11px] uppercase tracking-[0.08em] text-fg-subtle">
                  <Hash className="h-3 w-3" />
                  {type} configuration
                </div>
                <div className="grid grid-cols-1 gap-3 md:grid-cols-2">
                  {fields.map((f) => (
                    <Field key={f.key} label={f.label} hint={f.help}>
                      <TextInput
                        type={f.type || "text"}
                        placeholder={f.placeholder}
                        value={config[f.key] ?? ""}
                        onChange={(e) => setConfig({ ...config, [f.key]: e.target.value })}
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
              label="Enabled"
              hint="Disable to keep the channel configured but skip deliveries."
            />
          </FormSection>

          {error && <ErrorBox>{error}</ErrorBox>}

          <div className="flex items-center gap-2 pt-1">
            <Button variant="primary" type="submit" disabled={save.isPending}>
              {save.isPending ? "Saving…" : initial ? "Save" : "Create"}
            </Button>
            <Button type="button" onClick={onCancel}>
              Cancel
            </Button>
          </div>
        </form>
      </PanelBody>
    </Panel>
  );
}
