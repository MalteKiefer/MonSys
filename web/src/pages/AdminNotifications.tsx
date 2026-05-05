import { keepPreviousData, useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import {
  AlertTriangle,
  Bell,
  CheckCircle2,
  ChevronDown,
  ChevronRight,
  Code2,
  Hash,
  History,
  Mail,
  MessageCircle,
  MessageSquare,
  PencilLine,
  Plus,
  Send,
  Server,
  Trash2,
  Users,
  XCircle,
} from "lucide-react";
import { FormEvent, KeyboardEvent, useMemo, useRef, useState } from "react";

import {
  Button,
  ErrorBox,
  Field,
  Panel,
  PanelBody,
  PanelHeader,
  StatusPill,
  SuccessBox,
  TBody,
  TD,
  TH,
  THead,
  Table,
  TextInput,
} from "../components/ui";
import {
  CheckboxGrid,
  FormSection,
  PillGroup,
  TagChip,
  Toggle,
  TypeCardGrid,
} from "../components/notifications/FormParts";
import { api, ApiError } from "../lib/api";
import { useAuth } from "../lib/auth";
import { hostDisplay } from "../lib/utils";
import {
  AlertHistoryEntry,
  AuthConfig,
  ChannelType,
  Host,
  HostGroup,
  NotificationChannel,
  NotificationChannelInput,
  NotificationRule,
  NotificationRuleInput,
} from "../lib/types";

type Tab = "channels" | "rules" | "alerts";

export function AdminNotifications() {
  const user = useAuth((s) => s.user);
  const isAdmin = user?.role === "admin";
  const [tab, setTab] = useState<Tab>("channels");

  // Non-admins get Channels + Alerts (filtered to their channels server-side);
  // Rules stays admin-only since composing rules is an operator activity.
  const visibleTabs: Tab[] = isAdmin ? ["channels", "rules", "alerts"] : ["channels", "alerts"];

  return (
    <div className="mx-auto max-w-6xl space-y-5 p-6">
      <header className="flex items-center justify-between">
        <div>
          <h2 className="text-lg font-semibold tracking-tight">Notifications</h2>
          <p className="text-sm text-fg-muted">
            {isAdmin
              ? "Manage channels (email + webhooks), rules, and review alert history. Configure the global SMTP transport under Admin → Mail (SMTP)."
              : "Manage your delivery channels (email, Slack, Mattermost, Discord, ntfy) and view alerts on your channels. Email uses the SMTP transport set up by an admin."}
          </p>
        </div>
        <Tabs tab={tab} onChange={setTab} visible={visibleTabs} />
      </header>

      {tab === "channels" && (
        <div role="tabpanel" id="panel-channels" aria-labelledby="tab-channels">
          <ChannelsPanel isAdmin={!!isAdmin} myID={user?.id ?? ""} />
        </div>
      )}
      {isAdmin && tab === "rules" && (
        <div role="tabpanel" id="panel-rules" aria-labelledby="tab-rules">
          <RulesPanel />
        </div>
      )}
      {tab === "alerts" && (
        <div role="tabpanel" id="panel-alerts" aria-labelledby="tab-alerts">
          <AlertsPanel />
        </div>
      )}
    </div>
  );
}

function Tabs({ tab, onChange, visible }: { tab: Tab; onChange: (t: Tab) => void; visible: Tab[] }) {
  const allItems: Array<{ key: Tab; label: string; icon: typeof Bell }> = [
    { key: "channels", label: "Channels", icon: Bell },
    { key: "rules", label: "Rules", icon: AlertTriangle },
    { key: "alerts", label: "Alerts", icon: History },
  ];
  const items = allItems.filter((i) => visible.includes(i.key));
  const tablistRef = useRef<HTMLDivElement | null>(null);

  function onKeyDown(e: KeyboardEvent<HTMLDivElement>) {
    if (e.key !== "ArrowLeft" && e.key !== "ArrowRight") return;
    e.preventDefault();
    const idx = items.findIndex((i) => i.key === tab);
    if (idx < 0 || items.length === 0) return;
    const delta = e.key === "ArrowRight" ? 1 : -1;
    const next = items[(idx + delta + items.length) % items.length];
    onChange(next.key);
    const root = tablistRef.current;
    if (root) {
      const btn = root.querySelector<HTMLButtonElement>(`#tab-${next.key}`);
      btn?.focus();
    }
  }

  return (
    <div
      ref={tablistRef}
      role="tablist"
      onKeyDown={onKeyDown}
      className="inline-flex rounded-md border border-border bg-panel p-0.5"
    >
      {items.map(({ key, label, icon: Icon }) => {
        const active = tab === key;
        return (
          <button
            key={key}
            id={`tab-${key}`}
            role="tab"
            aria-selected={active}
            aria-controls={`panel-${key}`}
            tabIndex={active ? 0 : -1}
            onClick={() => onChange(key)}
            className={`inline-flex items-center gap-1.5 rounded px-2.5 py-1 text-xs font-medium transition-colors duration-150 ${
              active ? "bg-panel-2 text-fg shadow-panel" : "text-fg-subtle hover:text-fg"
            }`}
          >
            <Icon className="h-3.5 w-3.5" />
            {label}
          </button>
        );
      })}
    </div>
  );
}

// ---- Channels -------------------------------------------------------------

function ChannelsPanel({ isAdmin, myID }: { isAdmin: boolean; myID: string }) {
  const qc = useQueryClient();
  const list = useQuery({
    queryKey: ["channels"],
    queryFn: () => api<{ channels: NotificationChannel[] }>("/v1/notifications/channels"),
  });

  const [editing, setEditing] = useState<NotificationChannel | null>(null);
  const [creating, setCreating] = useState(false);

  return (
    <div className="space-y-5">
      {(creating || editing) && (
        <ChannelForm
          initial={editing}
          isAdmin={isAdmin}
          onCancel={() => {
            setEditing(null);
            setCreating(false);
          }}
          onSaved={() => {
            qc.invalidateQueries({ queryKey: ["channels"] });
            setEditing(null);
            setCreating(false);
          }}
        />
      )}

      <Panel>
        <PanelHeader>
          <h3 className="text-sm font-semibold">Channels</h3>
          <Button variant="primary" onClick={() => setCreating(true)}>
            <Plus className="h-3.5 w-3.5" /> New channel
          </Button>
        </PanelHeader>
        <PanelBody className="p-0 overflow-x-auto">
          {list.isLoading ? (
            <p className="px-5 py-4 text-sm text-fg-subtle">Loading…</p>
          ) : list.error ? (
            <ErrorBox>{(list.error as Error).message}</ErrorBox>
          ) : (list.data?.channels ?? []).length === 0 ? (
            <p className="px-5 py-8 text-center text-sm text-fg-subtle">No channels yet.</p>
          ) : (
            <Table>
              <THead>
                <tr>
                  <TH>Type</TH>
                  <TH>Name</TH>
                  <TH>Owner</TH>
                  <TH>Enabled</TH>
                  <TH>Last used</TH>
                  <TH>Last error</TH>
                  <TH className="text-right">Actions</TH>
                </tr>
              </THead>
              <TBody>
                {(list.data?.channels ?? []).map((c) => (
                  <ChannelRow
                    key={c.id}
                    channel={c}
                    isAdmin={isAdmin}
                    myID={myID}
                    onEdit={() => setEditing(c)}
                    onChange={() => qc.invalidateQueries({ queryKey: ["channels"] })}
                  />
                ))}
              </TBody>
            </Table>
          )}
        </PanelBody>
      </Panel>
    </div>
  );
}

function ChannelRow({
  channel,
  isAdmin,
  myID,
  onEdit,
  onChange,
}: {
  channel: NotificationChannel;
  isAdmin: boolean;
  myID: string;
  onEdit: () => void;
  onChange: () => void;
}) {
  const ownedByMe = channel.owner_user_id === myID;
  const canEdit = isAdmin || ownedByMe;
  const ownerLabel = !channel.owner_user_id
    ? "shared"
    : ownedByMe
      ? "you"
      : "other";
  const [testMsg, setTestMsg] = useState<{ kind: "ok" | "err"; text: string } | null>(null);

  const sendTest = useMutation({
    mutationFn: () =>
      api<{ ok: boolean; error?: string }>(`/v1/notifications/channels/${channel.id}/test`, {
        method: "POST",
        body: JSON.stringify({}),
      }),
    onSuccess: (data) => {
      if (data.ok) setTestMsg({ kind: "ok", text: "Test message sent." });
      else setTestMsg({ kind: "err", text: data.error ?? "Test failed." });
    },
    onError: (err) => setTestMsg({ kind: "err", text: err instanceof ApiError ? err.detail : "failed" }),
  });

  const del = useMutation({
    mutationFn: () => api(`/v1/notifications/channels/${channel.id}`, { method: "DELETE" }),
    onSuccess: onChange,
  });

  return (
    <>
      <tr className="hover:bg-panel-2">
        <TD className="text-fg-muted">{channel.type}</TD>
        <TD className="font-medium">{channel.name}</TD>
        <TD>
          <span className={`inline-flex rounded-md px-1.5 py-0.5 text-[11px] font-medium ${
            ownerLabel === "shared"
              ? "bg-info/10 text-info ring-1 ring-inset ring-info/30"
              : ownerLabel === "you"
                ? "bg-accent/10 text-accent ring-1 ring-inset ring-accent/30"
                : "bg-panel-2 text-fg-subtle ring-1 ring-inset ring-border"
          }`}>{ownerLabel}</span>
        </TD>
        <TD>
          <StatusPill status={channel.enabled ? "ok" : "offline"}>
            {channel.enabled ? "on" : "off"}
          </StatusPill>
        </TD>
        <TD className="text-fg-muted">
          {channel.last_used_at ? new Date(channel.last_used_at).toLocaleString() : "—"}
        </TD>
        <TD className="font-mono text-xs text-fg-subtle truncate max-w-xs">
          {channel.last_error || "—"}
        </TD>
        <TD className="text-right">
          <div className="inline-flex items-center gap-1">
            <Button onClick={() => sendTest.mutate()} disabled={sendTest.isPending || !canEdit}>
              <Send className="h-3.5 w-3.5" /> Test
            </Button>
            <Button onClick={onEdit} disabled={!canEdit}>
              <PencilLine className="h-3.5 w-3.5" /> Edit
            </Button>
            <Button
              variant="danger"
              disabled={!canEdit}
              onClick={() => {
                if (confirm(`Delete channel "${channel.name}"?`)) del.mutate();
              }}
            >
              <Trash2 className="h-3.5 w-3.5" />
            </Button>
          </div>
        </TD>
      </tr>
      {testMsg && (
        <tr className="bg-bg">
          <td colSpan={7} className="px-3 py-2 text-xs">
            {testMsg.kind === "ok" ? <SuccessBox>{testMsg.text}</SuccessBox> : <ErrorBox>{testMsg.text}</ErrorBox>}
          </td>
        </tr>
      )}
    </>
  );
}

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

function ChannelForm({
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

// ---- Rules ----------------------------------------------------------------

const RULE_CONDITIONS: Array<{ value: NotificationRule["condition_type"]; label: string; desc: string }> = [
  { value: "host_offline", label: "Host offline / stale", desc: "Fires on host_status transitions. Param: match_states (default ['offline'])." },
  { value: "monitor_failed", label: "Monitor failed", desc: "Fires when an active monitor returns status=fail. Optional params: monitor_type, monitor_name." },
  { value: "cert_expiring", label: "Cert expiring", desc: "Fires when a cert monitor returns warn or fail." },
  { value: "login_failed_threshold", label: "Failed logins threshold", desc: "Periodic check. Params: threshold (default 10), window_sec (default 300)." },
  { value: "security_updates_pending", label: "Security updates pending", desc: "Periodic. Param: threshold (default 1)." },
];

function RulesPanel() {
  const qc = useQueryClient();
  const rules = useQuery({
    queryKey: ["rules"],
    queryFn: () => api<{ rules: NotificationRule[] }>("/v1/notifications/rules"),
  });
  const channels = useQuery({
    queryKey: ["channels"],
    queryFn: () => api<{ channels: NotificationChannel[] }>("/v1/notifications/channels"),
  });

  const [editing, setEditing] = useState<NotificationRule | null>(null);
  const [creating, setCreating] = useState(false);

  return (
    <div className="space-y-5">
      {(creating || editing) && (
        <RuleForm
          key={editing?.id ?? "new"}
          initial={editing}
          channels={channels.data?.channels ?? []}
          onCancel={() => {
            setEditing(null);
            setCreating(false);
          }}
          onSaved={() => {
            qc.invalidateQueries({ queryKey: ["rules"] });
            setEditing(null);
            setCreating(false);
          }}
        />
      )}

      <Panel>
        <PanelHeader>
          <h3 className="text-sm font-semibold">Rules</h3>
          <Button
            variant="primary"
            disabled={(channels.data?.channels.length ?? 0) === 0}
            onClick={() => setCreating(true)}
          >
            <Plus className="h-3.5 w-3.5" /> New rule
          </Button>
        </PanelHeader>
        <PanelBody className="p-0 overflow-x-auto">
          {rules.isLoading ? (
            <p className="px-5 py-4 text-sm text-fg-subtle">Loading…</p>
          ) : (rules.data?.rules ?? []).length === 0 ? (
            <p className="px-5 py-8 text-center text-sm text-fg-subtle">
              {(channels.data?.channels.length ?? 0) === 0
                ? "Create a channel first, then add rules."
                : "No rules yet."}
            </p>
          ) : (
            <Table>
              <THead>
                <tr>
                  <TH>Name</TH>
                  <TH>Condition</TH>
                  <TH>Severity</TH>
                  <TH>Channels</TH>
                  <TH>Throttle</TH>
                  <TH>Enabled</TH>
                  <TH className="text-right">Actions</TH>
                </tr>
              </THead>
              <TBody>
                {(rules.data?.rules ?? []).map((r) => {
                  const chNames = r.channel_ids.map(
                    (id) => channels.data?.channels.find((c) => c.id === id)?.name ?? id.slice(0, 8),
                  );
                  return (
                    <tr key={r.id} className="hover:bg-panel-2">
                      <TD className="font-medium">{r.name}</TD>
                      <TD className="text-fg-muted">{r.condition_type}</TD>
                      <TD>
                        <StatusPill status={severityStatus(r.severity)}>{r.severity}</StatusPill>
                      </TD>
                      <TD className="text-fg-muted text-xs">{chNames.join(", ")}</TD>
                      <TD className="tabular-nums text-fg-muted">{r.throttle_sec}s</TD>
                      <TD>
                        <StatusPill status={r.enabled ? "ok" : "offline"}>
                          {r.enabled ? "on" : "off"}
                        </StatusPill>
                      </TD>
                      <TD className="text-right">
                        <div className="inline-flex items-center gap-1">
                          <Button onClick={() => setEditing(r)}>
                            <PencilLine className="h-3.5 w-3.5" /> Edit
                          </Button>
                          <Button
                            variant="danger"
                            onClick={() => {
                              if (confirm(`Delete rule "${r.name}"?`))
                                api(`/v1/notifications/rules/${r.id}`, { method: "DELETE" }).then(() =>
                                  qc.invalidateQueries({ queryKey: ["rules"] }),
                                );
                            }}
                          >
                            <Trash2 className="h-3.5 w-3.5" />
                          </Button>
                        </div>
                      </TD>
                    </tr>
                  );
                })}
              </TBody>
            </Table>
          )}
        </PanelBody>
      </Panel>
    </div>
  );
}

function severityStatus(s: NotificationRule["severity"]): "ok" | "warn" | "fail" {
  if (s === "info") return "ok";
  if (s === "warning") return "warn";
  return "fail";
}

function RuleForm({
  initial,
  channels,
  onCancel,
  onSaved,
}: {
  initial: NotificationRule | null;
  channels: NotificationChannel[];
  onCancel: () => void;
  onSaved: () => void;
}) {
  const tagsQuery = useQuery({
    queryKey: ["tags"],
    queryFn: () => api<{ tags: Array<{ tag: string; count: number }> }>("/v1/tags"),
  });
  const groupsQuery = useQuery({
    queryKey: ["groups"],
    queryFn: () => api<{ groups: HostGroup[] }>("/v1/groups"),
  });
  const hostsQuery = useQuery({
    queryKey: ["hosts"],
    queryFn: () => api<{ hosts: Host[] }>("/v1/hosts"),
  });

  const [name, setName] = useState(initial?.name ?? "");
  const [enabled, setEnabled] = useState(initial?.enabled ?? true);
  const [conditionType, setConditionType] = useState<NotificationRule["condition_type"]>(
    initial?.condition_type ?? "host_offline",
  );
  const [severity, setSeverity] = useState<NotificationRule["severity"]>(initial?.severity ?? "warning");
  const [throttleSec, setThrottleSec] = useState(initial?.throttle_sec ?? 300);
  const [repeatIntervalSec, setRepeatIntervalSec] = useState(initial?.repeat_interval_sec ?? 0);
  const [notifyOnResolve, setNotifyOnResolve] = useState(initial?.notify_on_resolve ?? true);
  const [tagsRaw, setTagsRaw] = useState((initial?.target_tags ?? []).join(", "));
  const [groupIDs, setGroupIDs] = useState<string[]>(initial?.target_group_ids ?? []);
  const [hostIDs, setHostIDs] = useState<string[]>(initial?.target_host_ids ?? []);
  const [paramsRaw, setParamsRaw] = useState(
    JSON.stringify(initial?.condition_params ?? {}, null, 2),
  );
  // Inline JSON-validation hint. Re-parsed on every keystroke so the user
  // sees the error before submitting; the server still validates the shape
  // on save, so we deliberately do not gate the submit button.
  const paramsParseError = useMemo<string | null>(() => {
    if (paramsRaw.trim() === "") return null;
    try {
      JSON.parse(paramsRaw);
      return null;
    } catch (e) {
      return (e as Error).message;
    }
  }, [paramsRaw]);
  const [channelIDs, setChannelIDs] = useState<string[]>(initial?.channel_ids ?? []);
  const [error, setError] = useState<string | null>(null);

  const save = useMutation({
    mutationFn: () => {
      let params: Record<string, unknown> = {};
      try {
        params = paramsRaw.trim() === "" ? {} : JSON.parse(paramsRaw);
      } catch (e) {
        throw new Error(`condition_params is not valid JSON: ${(e as Error).message}`);
      }
      if (channelIDs.length === 0) {
        throw new Error("Pick at least one channel");
      }
      if (repeatIntervalSec !== 0 && (repeatIntervalSec < 60 || repeatIntervalSec > 86400)) {
        throw new Error("Repeat interval must be 0 or between 60 and 86400 seconds");
      }
      const tagList = tagsRaw
        .split(",")
        .map((s) => s.trim().toLowerCase())
        .filter(Boolean);
      const body: NotificationRuleInput = {
        name,
        enabled,
        condition_type: conditionType,
        condition_params: params,
        channel_ids: channelIDs,
        severity,
        throttle_sec: throttleSec,
        repeat_interval_sec: repeatIntervalSec,
        notify_on_resolve: notifyOnResolve,
        target_host_ids: hostIDs,
        target_tags: tagList,
        target_group_ids: groupIDs,
      };
      if (initial) {
        return api(`/v1/notifications/rules/${initial.id}`, {
          method: "PUT",
          body: JSON.stringify(body),
        });
      }
      return api("/v1/notifications/rules", {
        method: "POST",
        body: JSON.stringify(body),
      });
    },
    onSuccess: onSaved,
    onError: (err) => setError(err instanceof ApiError ? err.detail : (err as Error).message),
  });

  function submit(e: FormEvent) {
    e.preventDefault();
    setError(null);
    save.mutate();
  }

  const conditionMeta = RULE_CONDITIONS.find((c) => c.value === conditionType)!;
  const conditionExample = CONDITION_PARAM_EXAMPLES[conditionType];

  // Tag-chip rendering: parse the comma-separated free-text input into chips
  // for visual feedback. The raw text remains the source of truth so the user
  // can keep typing mid-tag without losing input focus.
  const tagChips = useMemo(
    () =>
      tagsRaw
        .split(",")
        .map((s) => s.trim().toLowerCase())
        .filter(Boolean),
    [tagsRaw],
  );
  function removeTag(tag: string) {
    const next = tagChips.filter((t) => t !== tag);
    setTagsRaw(next.join(", "));
  }

  const channelOptions = channels.map((c) => ({
    id: c.id,
    primary: c.name,
    secondary: c.type,
    icon: channelIcon(c.type),
  }));

  const hostOptions = (hostsQuery.data?.hosts ?? []).map((h) => ({
    id: h.id,
    primary: hostDisplay(h),
    secondary:
      h.tags.length > 0 ? (
        <span className="inline-flex flex-wrap gap-1">
          {h.tags.map((t) => (
            <span
              key={t}
              className="rounded-md bg-panel-2 px-1 font-mono text-[10px] text-accent"
            >
              #{t}
            </span>
          ))}
        </span>
      ) : (
        <span className="text-fg-subtle">no tags</span>
      ),
    icon: Server,
  }));

  const groupOptions = (groupsQuery.data?.groups ?? []).map((g) => ({
    id: g.id,
    primary: g.name,
    secondary: `${g.member_ids.length} member${g.member_ids.length === 1 ? "" : "s"}`,
    icon: Users,
  }));

  return (
    <Panel>
      <PanelHeader>
        <h3 className="text-sm font-semibold">{initial ? `Edit ${initial.name}` : "New rule"}</h3>
      </PanelHeader>
      <PanelBody>
        <form onSubmit={submit} className="space-y-5">
          {/* --- Identification --- */}
          <FormSection label="Identification">
            <div className="grid grid-cols-1 gap-3 md:grid-cols-[1fr_auto] md:items-end">
              <Field label="Name">
                <TextInput
                  required
                  value={name}
                  onChange={(e) => setName(e.target.value)}
                  placeholder="e.g. prod hosts offline"
                />
              </Field>
              <Field label="Severity">
                <PillGroup
                  value={severity}
                  onChange={(v) => setSeverity(v)}
                  label="Severity"
                  options={[
                    {
                      value: "info",
                      label: "info",
                      activeClass: "bg-ok/15 text-ok ring-ok/30",
                    },
                    {
                      value: "warning",
                      label: "warning",
                      activeClass: "bg-warn/15 text-warn ring-warn/30",
                    },
                    {
                      value: "critical",
                      label: "critical",
                      activeClass: "bg-fail/15 text-fail ring-fail/30",
                    },
                  ]}
                />
              </Field>
            </div>
          </FormSection>

          {/* --- Trigger --- */}
          <FormSection label="Trigger">
            <Field label="Condition" hint={conditionMeta.desc}>
              <select
                value={conditionType}
                onChange={(e) => {
                  const next = e.target.value as NotificationRule["condition_type"];
                  setConditionType(next);
                  // If the user hasn't customised params, refresh the placeholder
                  // hint to match the new condition.
                  if (paramsRaw.trim() === "" || paramsRaw.trim() === "{}") {
                    setParamsRaw("");
                  }
                }}
                className="w-full rounded-md border border-border bg-panel px-3 py-2 text-sm focus:border-accent focus:outline-none"
              >
                {RULE_CONDITIONS.map((c) => (
                  <option key={c.value} value={c.value}>
                    {c.label}
                  </option>
                ))}
              </select>
            </Field>

            <AdvancedParams
              value={paramsRaw}
              onChange={setParamsRaw}
              placeholder={conditionExample}
              parseError={paramsParseError}
            />
          </FormSection>

          {/* --- Targets --- */}
          <FormSection
            label="Targets"
            hint="Empty = every host. Otherwise host must match any selection."
          >
            <Field label={`Hosts (${hostIDs.length} selected)`}>
              <CheckboxGrid
                options={hostOptions}
                selected={hostIDs}
                onToggle={(id) =>
                  setHostIDs((prev) =>
                    prev.includes(id) ? prev.filter((x) => x !== id) : [...prev, id],
                  )
                }
                empty="No hosts known yet."
                maxHeight="max-h-56"
              />
            </Field>

            <div className="grid grid-cols-1 gap-3 md:grid-cols-2">
              <Field
                label="Tags"
                hint={
                  tagsQuery.data?.tags?.length
                    ? `Existing: ${tagsQuery.data.tags.map((t) => t.tag).join(", ")}`
                    : "No tags defined yet."
                }
              >
                <TextInput
                  value={tagsRaw}
                  onChange={(e) => setTagsRaw(e.target.value)}
                  placeholder="prod, db"
                  className="font-mono"
                />
                {tagChips.length > 0 && (
                  <div className="mt-2 flex flex-wrap gap-1">
                    {tagChips.map((t) => (
                      <TagChip key={t} text={t} onRemove={() => removeTag(t)} />
                    ))}
                  </div>
                )}
              </Field>

              <Field label={`Groups (${groupIDs.length} selected)`}>
                <CheckboxGrid
                  options={groupOptions}
                  selected={groupIDs}
                  onToggle={(id) =>
                    setGroupIDs((prev) =>
                      prev.includes(id) ? prev.filter((x) => x !== id) : [...prev, id],
                    )
                  }
                  empty="No groups defined yet."
                  maxHeight="max-h-40"
                  columns="grid-cols-1"
                />
              </Field>
            </div>
          </FormSection>

          {/* --- Delivery --- */}
          <FormSection label="Delivery">
            <Field
              label={`Channels (${channelIDs.length} selected)`}
              hint="At least one channel must be selected."
            >
              <CheckboxGrid
                options={channelOptions}
                selected={channelIDs}
                onToggle={(id) =>
                  setChannelIDs((prev) =>
                    prev.includes(id) ? prev.filter((x) => x !== id) : [...prev, id],
                  )
                }
                empty="No channels available."
                maxHeight="max-h-56"
              />
            </Field>
          </FormSection>

          {/* --- Cadence --- */}
          <FormSection label="Cadence" divider={false}>
            <div className="grid grid-cols-1 gap-3 md:grid-cols-2">
              <Field label="Throttle (seconds)" hint="0 disables throttle.">
                <TextInput
                  type="number"
                  min={0}
                  value={throttleSec}
                  onChange={(e) => setThrottleSec(parseInt(e.target.value || "0", 10))}
                />
              </Field>
              <Field
                label="Repeat reminder (seconds)"
                hint="0 = fire once per outage. 60–86400 = re-send while still active."
              >
                <TextInput
                  type="number"
                  min={0}
                  max={86400}
                  value={repeatIntervalSec}
                  onChange={(e) => setRepeatIntervalSec(parseInt(e.target.value || "0", 10))}
                />
              </Field>
            </div>
            <div className="grid grid-cols-1 gap-3 rounded-md border border-border bg-panel-2/40 p-3 md:grid-cols-2">
              <Toggle
                checked={notifyOnResolve}
                onChange={setNotifyOnResolve}
                label="Notify on resolve"
                hint="Send an all-clear when the host or monitor recovers."
              />
              <Toggle
                checked={enabled}
                onChange={setEnabled}
                label="Enabled"
                hint="Disable to keep the rule but stop firing alerts."
              />
            </div>
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

// Per-condition placeholder for the advanced params textarea. Mirrors the
// docstring on each RULE_CONDITIONS entry; shown only as a hint, never
// pre-filled into state so an empty body still serialises as `{}`.
const CONDITION_PARAM_EXAMPLES: Record<NotificationRule["condition_type"], string> = {
  host_offline: '{\n  "match_states": ["offline", "stale"]\n}',
  monitor_failed: '{\n  "monitor_type": "http",\n  "monitor_name": "api"\n}',
  cert_expiring: "{}",
  login_failed_threshold: '{\n  "threshold": 10,\n  "window_sec": 300\n}',
  security_updates_pending: '{\n  "threshold": 1\n}',
};

// Lucide icon picker for channel types. Keep in sync with CHANNEL_TYPE_CARDS.
function channelIcon(type: ChannelType) {
  switch (type) {
    case "email":
      return Mail;
    case "slack":
      return MessageSquare;
    case "mattermost":
    case "discord":
      return MessageCircle;
    case "ntfy":
      return Bell;
    default:
      return Bell;
  }
}

// Collapsible "Advanced" expander housing the JSON params textarea.
// Closed by default for new rules; auto-opens when editing a rule that has
// non-empty params (so the user can see what's there) or when the JSON fails
// to parse (so the inline error isn't hidden behind a closed pane).
function AdvancedParams({
  value,
  onChange,
  placeholder,
  parseError,
}: {
  value: string;
  onChange: (v: string) => void;
  placeholder: string;
  parseError: string | null;
}) {
  const hasContent = value.trim() !== "" && value.trim() !== "{}";
  const [open, setOpen] = useState<boolean>(hasContent || !!parseError);

  return (
    <div className="rounded-md border border-border bg-panel-2/40">
      <button
        type="button"
        onClick={() => setOpen((o) => !o)}
        className="flex w-full items-center justify-between gap-2 px-3 py-2 text-left text-xs font-medium text-fg-muted hover:text-fg"
      >
        <span className="inline-flex items-center gap-2">
          <Code2 className="h-3.5 w-3.5" />
          Advanced — condition params (JSON)
          {hasContent && (
            <span className="rounded bg-accent/15 px-1.5 py-0.5 font-mono text-[10px] text-accent">
              custom
            </span>
          )}
          {parseError && (
            <span className="rounded bg-fail/15 px-1.5 py-0.5 text-[10px] text-fail">
              invalid
            </span>
          )}
        </span>
        {open ? (
          <ChevronDown className="h-3.5 w-3.5 text-fg-subtle" />
        ) : (
          <ChevronRight className="h-3.5 w-3.5 text-fg-subtle" />
        )}
      </button>
      {open && (
        <div className="border-t border-border px-3 pb-3 pt-2">
          <textarea
            rows={5}
            value={value}
            onChange={(e) => onChange(e.target.value)}
            placeholder={placeholder}
            className="w-full rounded-md border border-border bg-panel px-3 py-2 font-mono text-xs text-fg placeholder:text-fg-subtle focus:border-accent focus:outline-none focus:ring-2 focus:ring-accent/30"
          />
          {parseError ? (
            <p className="mt-1 text-xs text-fail">Invalid JSON: {parseError}</p>
          ) : (
            <p className="mt-1 text-[11px] text-fg-subtle">
              Leave blank to use the condition defaults.
            </p>
          )}
        </div>
      )}
    </div>
  );
}

// ---- Alert history --------------------------------------------------------

// Dedup-key prefixes that embed a host id as the trailing segment. Other
// alert types (monitor_failed, cert_expiring, security_updates_pending) key
// off a monitor id or are global, so they cannot be matched to a single host
// and are hidden whenever a specific host is selected.
const HOST_SCOPED_DEDUP_PREFIXES = ["host_offline:", "host_login_failed:"] as const;

function AlertsPanel() {
  const [since, setSince] = useState("24h");
  const [hostFilter, setHostFilter] = useState<string>("");
  const sinceISO = useMemo(() => {
    const map: Record<string, number> = { "1h": 3600, "24h": 86400, "7d": 604800, "30d": 2592000 };
    const sec = map[since] ?? 86400;
    return new Date(Date.now() - sec * 1000).toISOString();
  }, [since]);

  const list = useQuery({
    queryKey: ["alerts", sinceISO],
    queryFn: () =>
      api<{ alerts: AlertHistoryEntry[] }>(
        `/v1/notifications/alerts?since=${encodeURIComponent(sinceISO)}&limit=200`,
      ),
    placeholderData: keepPreviousData,
    refetchInterval: 30_000,
  });

  const hostsQuery = useQuery({
    queryKey: ["hosts"],
    queryFn: () => api<{ hosts: Host[] }>("/v1/hosts"),
  });

  const allAlerts = list.data?.alerts ?? [];
  const filteredAlerts = useMemo(() => {
    if (!hostFilter) return allAlerts;
    const suffix = `:${hostFilter}`;
    return allAlerts.filter((a) => {
      const key = a.dedup_key ?? "";
      // Only host-scoped alert types carry a host id in dedup_key. For other
      // types (monitor_failed, cert_expiring, security_updates_pending) we
      // can't tie them to a specific host, so suppress them when filtering.
      const isHostScoped = HOST_SCOPED_DEDUP_PREFIXES.some((p) => key.startsWith(p));
      return isHostScoped && key.endsWith(suffix);
    });
  }, [allAlerts, hostFilter]);

  const sortedHosts = useMemo(() => {
    const hs = [...(hostsQuery.data?.hosts ?? [])];
    hs.sort((a, b) => hostDisplay(a).localeCompare(hostDisplay(b)));
    return hs;
  }, [hostsQuery.data]);

  return (
    <Panel>
      <PanelHeader>
        <h3 className="text-sm font-semibold">Alert history</h3>
        <div className="inline-flex rounded-md border border-border bg-panel p-0.5">
          {(["1h", "24h", "7d", "30d"] as const).map((s) => (
            <button
              key={s}
              onClick={() => setSince(s)}
              className={`rounded px-2.5 py-1 text-xs font-medium transition-colors duration-150 ${
                since === s ? "bg-panel-2 text-fg shadow-panel" : "text-fg-subtle hover:text-fg"
              }`}
            >
              {s}
            </button>
          ))}
        </div>
      </PanelHeader>
      <PanelBody className="p-0 overflow-x-auto">
        <div className="flex flex-wrap items-end gap-3 px-5 py-3 border-b border-border">
          <Field label="Filter by host" hint="Only host-scoped alerts (offline, login-failed) will match.">
            <select
              value={hostFilter}
              onChange={(e) => setHostFilter(e.target.value)}
              className="w-full rounded-md border border-border bg-panel px-3 py-2 text-sm focus:border-accent focus:outline-none md:w-72"
            >
              <option value="">All hosts</option>
              {sortedHosts.map((h) => (
                <option key={h.id} value={h.id}>
                  {hostDisplay(h)}
                </option>
              ))}
            </select>
          </Field>
          <p className="pb-2 text-xs text-fg-subtle tabular-nums">
            {hostFilter && allAlerts.length !== filteredAlerts.length
              ? `${filteredAlerts.length} of ${allAlerts.length}`
              : `${allAlerts.length} alert${allAlerts.length === 1 ? "" : "s"}`}
          </p>
        </div>
        {list.isLoading ? (
          <p className="px-5 py-4 text-sm text-fg-subtle">Loading…</p>
        ) : filteredAlerts.length === 0 ? (
          <p className="px-5 py-8 text-center text-sm text-fg-subtle">
            {allAlerts.length === 0
              ? "No alerts in this window."
              : "No alerts match the selected host."}
          </p>
        ) : (
          <Table>
            <THead>
              <tr>
                <TH>When</TH>
                <TH>Severity</TH>
                <TH>Rule</TH>
                <TH>Subject</TH>
                <TH>Delivered</TH>
              </tr>
            </THead>
            <TBody>
              {filteredAlerts.map((a) => {
                const errorCount = Object.keys(a.delivery_errors ?? {}).length;
                return (
                  <tr key={a.id} className="hover:bg-panel-2">
                    <TD className="font-mono text-xs text-fg-muted">{relTime(a.at)}</TD>
                    <TD>
                      <StatusPill status={severityStatus(a.severity as NotificationRule["severity"])}>
                        {a.severity}
                      </StatusPill>
                    </TD>
                    <TD className="text-fg-muted">{a.rule_name}</TD>
                    <TD>{a.subject}</TD>
                    <TD className="text-fg-muted">
                      {a.delivered_to.length > 0 ? (
                        <span className="inline-flex items-center gap-1 text-ok">
                          <CheckCircle2 className="h-3.5 w-3.5" />
                          {a.delivered_to.length}
                        </span>
                      ) : null}
                      {errorCount > 0 && (
                        <span className="ml-2 inline-flex items-center gap-1 text-fail">
                          <XCircle className="h-3.5 w-3.5" />
                          {errorCount}
                        </span>
                      )}
                    </TD>
                  </tr>
                );
              })}
            </TBody>
          </Table>
        )}
      </PanelBody>
    </Panel>
  );
}

function relTime(iso: string): string {
  const t = new Date(iso).getTime();
  if (Number.isNaN(t)) return iso;
  const diff = (Date.now() - t) / 1000;
  if (diff < 60) return `${Math.round(diff)}s ago`;
  if (diff < 3600) return `${Math.round(diff / 60)}m ago`;
  if (diff < 86400) return `${Math.round(diff / 3600)}h ago`;
  return `${Math.round(diff / 86400)}d ago`;
}
