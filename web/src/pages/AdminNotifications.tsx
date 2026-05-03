import { keepPreviousData, useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import {
  AlertTriangle,
  Bell,
  CheckCircle2,
  History,
  PencilLine,
  Plus,
  Send,
  Trash2,
  XCircle,
} from "lucide-react";
import { FormEvent, useMemo, useState } from "react";

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
import { api, ApiError } from "../lib/api";
import {
  AlertHistoryEntry,
  NotificationChannel,
  NotificationChannelInput,
  NotificationRule,
  NotificationRuleInput,
} from "../lib/types";

type Tab = "channels" | "rules" | "alerts";

export function AdminNotifications() {
  const [tab, setTab] = useState<Tab>("channels");
  return (
    <div className="mx-auto max-w-6xl space-y-5 p-6">
      <header className="flex items-center justify-between">
        <div>
          <h2 className="text-lg font-semibold tracking-tight">Notifications</h2>
          <p className="text-sm text-fg-muted">
            Configure channels (SMTP / Slack / Mattermost / ntfy), rules, and review alert history.
          </p>
        </div>
        <Tabs tab={tab} onChange={setTab} />
      </header>

      {tab === "channels" && <ChannelsPanel />}
      {tab === "rules" && <RulesPanel />}
      {tab === "alerts" && <AlertsPanel />}
    </div>
  );
}

function Tabs({ tab, onChange }: { tab: Tab; onChange: (t: Tab) => void }) {
  const items: Array<{ key: Tab; label: string; icon: typeof Bell }> = [
    { key: "channels", label: "Channels", icon: Bell },
    { key: "rules", label: "Rules", icon: AlertTriangle },
    { key: "alerts", label: "Alerts", icon: History },
  ];
  return (
    <div role="tablist" className="inline-flex rounded-md border border-border bg-panel p-0.5">
      {items.map(({ key, label, icon: Icon }) => {
        const active = tab === key;
        return (
          <button
            key={key}
            role="tab"
            aria-selected={active}
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

function ChannelsPanel() {
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
  onEdit,
  onChange,
}: {
  channel: NotificationChannel;
  onEdit: () => void;
  onChange: () => void;
}) {
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
            <Button onClick={() => sendTest.mutate()} disabled={sendTest.isPending}>
              <Send className="h-3.5 w-3.5" /> Test
            </Button>
            <Button onClick={onEdit}>
              <PencilLine className="h-3.5 w-3.5" /> Edit
            </Button>
            <Button
              variant="danger"
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
          <td colSpan={6} className="px-3 py-2 text-xs">
            {testMsg.kind === "ok" ? <SuccessBox>{testMsg.text}</SuccessBox> : <ErrorBox>{testMsg.text}</ErrorBox>}
          </td>
        </tr>
      )}
    </>
  );
}

// Channel-form schema by type. Sensitive fields shown as password inputs.
const CHANNEL_FIELDS: Record<NotificationChannel["type"], Array<{ key: string; label: string; type?: string; placeholder?: string; help?: string }>> = {
  smtp: [
    { key: "host", label: "Host", placeholder: "smtp.example.com" },
    { key: "port", label: "Port", placeholder: "587" },
    { key: "username", label: "Username (optional)" },
    { key: "password", label: "Password (optional)", type: "password" },
    { key: "from", label: "From", placeholder: "alerts@example.com" },
    { key: "to", label: "To (comma-separated)", placeholder: "ops@example.com,admin@example.com" },
    { key: "starttls", label: "STARTTLS (true/false)", placeholder: "true" },
    { key: "tls", label: "Implicit TLS (true/false)", placeholder: "false" },
  ],
  slack: [
    { key: "webhook_url", label: "Incoming webhook URL", type: "password", placeholder: "https://hooks.slack.com/…" },
  ],
  mattermost: [
    { key: "webhook_url", label: "Incoming webhook URL", type: "password" },
    { key: "username", label: "Display username (optional)", placeholder: "mon" },
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
  onCancel,
  onSaved,
}: {
  initial: NotificationChannel | null;
  onCancel: () => void;
  onSaved: () => void;
}) {
  const [type, setType] = useState<NotificationChannel["type"]>(initial?.type ?? "ntfy");
  const [name, setName] = useState(initial?.name ?? "");
  const [enabled, setEnabled] = useState(initial?.enabled ?? true);
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
      const parsed = parseConfig(type, config);
      const body: NotificationChannelInput = { type, name, enabled, config: parsed };
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

  const fields = CHANNEL_FIELDS[type];

  return (
    <Panel>
      <PanelHeader>
        <h3 className="text-sm font-semibold">{initial ? `Edit ${initial.name}` : "New channel"}</h3>
      </PanelHeader>
      <PanelBody>
        <form onSubmit={submit} className="space-y-4">
          <div className="grid grid-cols-2 gap-3">
            <Field label="Type">
              <select
                value={type}
                disabled={!!initial}
                onChange={(e) => {
                  setType(e.target.value as NotificationChannel["type"]);
                  setConfig({});
                }}
                className="w-full rounded-md border border-border bg-panel px-3 py-2 text-sm focus:border-accent focus:outline-none disabled:opacity-60"
              >
                <option value="ntfy">ntfy</option>
                <option value="slack">slack</option>
                <option value="mattermost">mattermost</option>
                <option value="smtp">smtp</option>
              </select>
            </Field>
            <Field label="Name">
              <TextInput required value={name} onChange={(e) => setName(e.target.value)} />
            </Field>
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

          <label className="flex items-center gap-2 text-sm text-fg-muted">
            <input type="checkbox" checked={enabled} onChange={(e) => setEnabled(e.target.checked)} />
            Enabled
          </label>

          {error && <ErrorBox>{error}</ErrorBox>}

          <div className="flex items-center gap-2">
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

function parseConfig(type: NotificationChannel["type"], cfg: Record<string, string>): Record<string, unknown> {
  const out: Record<string, unknown> = {};
  for (const [k, v] of Object.entries(cfg)) {
    if (v === "") continue;
    if (k === "port") {
      const n = Number(v);
      out[k] = Number.isFinite(n) ? n : v;
    } else if (k === "starttls" || k === "tls" || k === "insecure_skip_verify") {
      out[k] = v.toLowerCase() === "true";
    } else if (type === "smtp" && k === "to") {
      out[k] = v.split(",").map((s) => s.trim()).filter(Boolean);
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
  const [name, setName] = useState(initial?.name ?? "");
  const [enabled, setEnabled] = useState(initial?.enabled ?? true);
  const [conditionType, setConditionType] = useState<NotificationRule["condition_type"]>(
    initial?.condition_type ?? "host_offline",
  );
  const [severity, setSeverity] = useState<NotificationRule["severity"]>(initial?.severity ?? "warning");
  const [throttleSec, setThrottleSec] = useState(initial?.throttle_sec ?? 300);
  const [paramsRaw, setParamsRaw] = useState(
    JSON.stringify(initial?.condition_params ?? {}, null, 2),
  );
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
      const body: NotificationRuleInput = {
        name,
        enabled,
        condition_type: conditionType,
        condition_params: params,
        channel_ids: channelIDs,
        severity,
        throttle_sec: throttleSec,
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

  return (
    <Panel>
      <PanelHeader>
        <h3 className="text-sm font-semibold">{initial ? `Edit ${initial.name}` : "New rule"}</h3>
      </PanelHeader>
      <PanelBody>
        <form onSubmit={submit} className="space-y-4">
          <div className="grid grid-cols-2 gap-3">
            <Field label="Name">
              <TextInput required value={name} onChange={(e) => setName(e.target.value)} />
            </Field>
            <Field label="Severity">
              <select
                value={severity}
                onChange={(e) => setSeverity(e.target.value as NotificationRule["severity"])}
                className="w-full rounded-md border border-border bg-panel px-3 py-2 text-sm"
              >
                <option value="info">info</option>
                <option value="warning">warning</option>
                <option value="critical">critical</option>
              </select>
            </Field>
          </div>

          <Field label="Condition" hint={conditionMeta.desc}>
            <select
              value={conditionType}
              onChange={(e) => setConditionType(e.target.value as NotificationRule["condition_type"])}
              className="w-full rounded-md border border-border bg-panel px-3 py-2 text-sm"
            >
              {RULE_CONDITIONS.map((c) => (
                <option key={c.value} value={c.value}>
                  {c.label}
                </option>
              ))}
            </select>
          </Field>

          <Field
            label="Condition params (JSON)"
            hint='Examples: {"match_states":["offline","stale"]} · {"threshold":10,"window_sec":300} · {}'
          >
            <textarea
              rows={4}
              value={paramsRaw}
              onChange={(e) => setParamsRaw(e.target.value)}
              className="w-full rounded-md border border-border bg-panel px-3 py-2 font-mono text-xs text-fg focus:border-accent focus:outline-none"
            />
          </Field>

          <Field label="Channels" hint="Select one or more delivery channels (Ctrl/⌘ to multi-select).">
            <select
              multiple
              size={Math.min(6, Math.max(3, channels.length))}
              value={channelIDs}
              onChange={(e) =>
                setChannelIDs(Array.from(e.target.selectedOptions).map((o) => o.value))
              }
              className="w-full rounded-md border border-border bg-panel px-3 py-2 text-sm"
            >
              {channels.map((c) => (
                <option key={c.id} value={c.id}>
                  {c.type} · {c.name}
                </option>
              ))}
            </select>
          </Field>

          <div className="grid grid-cols-2 gap-3">
            <Field label="Throttle (seconds)" hint="0 disables throttle.">
              <TextInput
                type="number"
                min={0}
                value={throttleSec}
                onChange={(e) => setThrottleSec(parseInt(e.target.value || "0", 10))}
              />
            </Field>
            <Field label="Enabled">
              <label className="mt-2 inline-flex items-center gap-2 text-sm text-fg-muted">
                <input type="checkbox" checked={enabled} onChange={(e) => setEnabled(e.target.checked)} />
                On
              </label>
            </Field>
          </div>

          {error && <ErrorBox>{error}</ErrorBox>}

          <div className="flex items-center gap-2">
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

// ---- Alert history --------------------------------------------------------

function AlertsPanel() {
  const [since, setSince] = useState("24h");
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
        {list.isLoading ? (
          <p className="px-5 py-4 text-sm text-fg-subtle">Loading…</p>
        ) : (list.data?.alerts ?? []).length === 0 ? (
          <p className="px-5 py-8 text-center text-sm text-fg-subtle">No alerts in this window.</p>
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
              {(list.data?.alerts ?? []).map((a) => {
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
