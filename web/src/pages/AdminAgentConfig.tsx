import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { Eye, PencilLine, Plus, Settings, Trash2 } from "lucide-react";
import { FormEvent, useState } from "react";

import {
  Button,
  Empty,
  ErrorBox,
  Field,
  Panel,
  PanelBody,
  PanelHeader,
  StatusPill,
  TBody,
  TD,
  TH,
  THead,
  Table,
  TextInput,
} from "../components/ui";
import { api, ApiError } from "../lib/api";
import {
  AgentConfig,
  AgentConfigEntry,
  AgentConfigInput,
  AgentConfigResolved,
  Host,
  HostGroup,
} from "../lib/types";

const DAYS = ["Sun", "Mon", "Tue", "Wed", "Thu", "Fri", "Sat"];

export function AdminAgentConfig() {
  const qc = useQueryClient();
  const list = useQuery({
    queryKey: ["agent-configs"],
    queryFn: () => api<{ configs: AgentConfigEntry[] }>("/v1/admin/agent-config"),
  });
  const hosts = useQuery({
    queryKey: ["hosts"],
    queryFn: () => api<{ hosts: Host[] }>("/v1/hosts"),
  });
  const groups = useQuery({
    queryKey: ["groups"],
    queryFn: () => api<{ groups: HostGroup[] }>("/v1/groups"),
  });

  const [editing, setEditing] = useState<AgentConfigEntry | null>(null);
  const [creating, setCreating] = useState(false);
  const [selectedHostID, setSelectedHostID] = useState<string>("");
  const [previewHost, setPreviewHost] = useState<Host | null>(null);

  return (
    <div className="mx-auto max-w-6xl space-y-5 p-6">
      <header>
        <h2 className="text-lg font-semibold tracking-tight">Agent configuration</h2>
        <p className="text-sm text-fg-muted">
          Server-managed knobs the agent applies after first start. Host
          overrides take precedence over group overrides, group overrides over
          global. Missing keys fall back to the agent's compiled defaults.
        </p>
      </header>

      {(creating || editing) && (
        <ConfigForm
          initial={editing}
          hosts={hosts.data?.hosts ?? []}
          groups={groups.data?.groups ?? []}
          onCancel={() => {
            setEditing(null);
            setCreating(false);
          }}
          onSaved={() => {
            qc.invalidateQueries({ queryKey: ["agent-configs"] });
            setEditing(null);
            setCreating(false);
          }}
        />
      )}

      {previewHost && <PreviewPanel host={previewHost} onClose={() => setPreviewHost(null)} />}

      <Panel>
        <PanelHeader>
          <div className="flex items-center gap-2">
            <Settings className="h-4 w-4 text-fg-muted" />
            <h3 className="text-sm font-semibold">Configurations</h3>
          </div>
          <div className="flex items-center gap-2">
            <select
              value={selectedHostID}
              onChange={(e) => setSelectedHostID(e.target.value)}
              className="rounded-md border border-border bg-panel px-2 py-1 text-xs"
            >
              <option value="">Preview merged config…</option>
              {(hosts.data?.hosts ?? []).map((h) => (
                <option key={h.id} value={h.id}>{h.hostname}</option>
              ))}
            </select>
            <Button
              disabled={!selectedHostID}
              onClick={() => {
                const h = (hosts.data?.hosts ?? []).find((x) => x.id === selectedHostID);
                if (h) setPreviewHost(h);
              }}
            >
              <Eye className="h-3.5 w-3.5" /> Preview
            </Button>
            <Button variant="primary" onClick={() => setCreating(true)}>
              <Plus className="h-3.5 w-3.5" /> New
            </Button>
          </div>
        </PanelHeader>
        <PanelBody className="p-0 overflow-x-auto">
          {list.isLoading ? (
            <p className="px-5 py-4 text-sm text-fg-subtle">Loading…</p>
          ) : (list.data?.configs ?? []).length === 0 ? (
            <Empty>No configs yet.</Empty>
          ) : (
            <Table>
              <THead>
                <tr>
                  <TH>Scope</TH>
                  <TH>Target</TH>
                  <TH>Interval</TH>
                  <TH>Quiet hours</TH>
                  <TH>Description</TH>
                  <TH>Enabled</TH>
                  <TH className="text-right">Actions</TH>
                </tr>
              </THead>
              <TBody>
                {(list.data?.configs ?? []).map((c) => (
                  <tr key={c.id} className="hover:bg-panel-2">
                    <TD>
                      <StatusPill status={scopeColor(c.scope)}>{c.scope}</StatusPill>
                    </TD>
                    <TD className="font-mono text-xs">
                      {c.scope === "global" ? "—" : c.target_name || c.target_id?.slice(0, 8)}
                    </TD>
                    <TD className="tabular-nums text-fg-muted">
                      {c.config.interval_seconds ? `${c.config.interval_seconds}s` : "—"}
                    </TD>
                    <TD className="font-mono text-xs text-fg-muted">
                      {c.config.quiet_hours?.enabled
                        ? `${c.config.quiet_hours.start}–${c.config.quiet_hours.end}`
                        : "—"}
                    </TD>
                    <TD className="text-fg-muted truncate max-w-xs">{c.description || "—"}</TD>
                    <TD>
                      <StatusPill status={c.enabled ? "ok" : "offline"}>{c.enabled ? "on" : "off"}</StatusPill>
                    </TD>
                    <TD className="text-right">
                      <div className="inline-flex items-center gap-1">
                        <Button onClick={() => setEditing(c)}>
                          <PencilLine className="h-3.5 w-3.5" /> Edit
                        </Button>
                        <Button
                          variant="danger"
                          onClick={() => {
                            if (confirm(`Delete config (${c.scope}${c.target_name ? "/" + c.target_name : ""})?`))
                              api(`/v1/admin/agent-config/${c.id}`, { method: "DELETE" }).then(() =>
                                qc.invalidateQueries({ queryKey: ["agent-configs"] }),
                              );
                          }}
                        >
                          <Trash2 className="h-3.5 w-3.5" />
                        </Button>
                      </div>
                    </TD>
                  </tr>
                ))}
              </TBody>
            </Table>
          )}
        </PanelBody>
      </Panel>
    </div>
  );
}

function scopeColor(scope: AgentConfigEntry["scope"]): "ok" | "warn" | "info" {
  if (scope === "global") return "info";
  if (scope === "group") return "warn";
  return "ok";
}

function PreviewPanel({ host, onClose }: { host: Host; onClose: () => void }) {
  const preview = useQuery({
    queryKey: ["agent-config-preview", host.id],
    queryFn: () => api<AgentConfigResolved>(`/v1/admin/agent-config/preview/${host.id}`),
  });
  return (
    <Panel>
      <PanelHeader>
        <div className="flex items-center gap-2">
          <Eye className="h-4 w-4 text-fg-muted" />
          <h3 className="text-sm font-semibold">Preview · {host.hostname}</h3>
        </div>
        <Button onClick={onClose}>Close</Button>
      </PanelHeader>
      <PanelBody>
        {preview.isLoading ? (
          <p className="text-sm text-fg-subtle">Resolving…</p>
        ) : preview.error ? (
          <ErrorBox>{(preview.error as Error).message}</ErrorBox>
        ) : (
          <>
            <p className="mb-2 text-xs text-fg-subtle">
              Sources merged: <span className="font-mono text-fg-muted">{preview.data?.source_scopes.join(" → ") || "(defaults only)"}</span>
            </p>
            <pre className="rounded-md border border-border bg-bg p-3 font-mono text-[11px] leading-relaxed text-fg overflow-auto">
{JSON.stringify(preview.data?.config ?? {}, null, 2)}
            </pre>
          </>
        )}
      </PanelBody>
    </Panel>
  );
}

function ConfigForm({
  initial,
  hosts,
  groups,
  onCancel,
  onSaved,
}: {
  initial: AgentConfigEntry | null;
  hosts: Host[];
  groups: HostGroup[];
  onCancel: () => void;
  onSaved: () => void;
}) {
  const [scope, setScope] = useState<AgentConfigEntry["scope"]>(initial?.scope ?? "global");
  const [targetID, setTargetID] = useState<string>(initial?.target_id ?? "");
  const [enabled, setEnabled] = useState(initial?.enabled ?? true);
  const [description, setDescription] = useState(initial?.description ?? "");
  const [intervalSec, setIntervalSec] = useState<string>(
    initial?.config.interval_seconds != null ? String(initial.config.interval_seconds) : "",
  );
  const [bufferMB, setBufferMB] = useState<string>(
    initial?.config.buffer_max_mb != null ? String(initial.config.buffer_max_mb) : "",
  );

  const initPkg = initial?.config.packages ?? {};
  const [pkgEnabled, setPkgEnabled] = useState<"" | "yes" | "no">(
    initPkg.enabled === true ? "yes" : initPkg.enabled === false ? "no" : "",
  );
  const [pkgUpdateInterval, setPkgUpdateInterval] = useState(initPkg.update_check_interval ?? "");
  const [pkgFullSnapshot, setPkgFullSnapshot] = useState(initPkg.full_snapshot_max_interval ?? "");

  const initQH = initial?.config.quiet_hours;
  const [qhEnabled, setQhEnabled] = useState(initQH?.enabled ?? false);
  const [qhStart, setQhStart] = useState(initQH?.start ?? "22:00");
  const [qhEnd, setQhEnd] = useState(initQH?.end ?? "06:00");
  const [qhDays, setQhDays] = useState<number[]>(initQH?.days ?? []);

  const [error, setError] = useState<string | null>(null);

  const save = useMutation({
    mutationFn: () => {
      if (scope !== "global" && !targetID) {
        throw new Error(`scope=${scope} requires a target`);
      }
      const cfg: AgentConfig = {};
      if (intervalSec !== "") cfg.interval_seconds = parseInt(intervalSec, 10);
      if (bufferMB !== "") cfg.buffer_max_mb = parseInt(bufferMB, 10);
      const pkg: AgentConfig["packages"] = {};
      if (pkgEnabled !== "") pkg.enabled = pkgEnabled === "yes";
      if (pkgUpdateInterval) pkg.update_check_interval = pkgUpdateInterval;
      if (pkgFullSnapshot) pkg.full_snapshot_max_interval = pkgFullSnapshot;
      if (Object.keys(pkg).length > 0) cfg.packages = pkg;
      if (qhEnabled || qhStart !== "" || qhEnd !== "") {
        cfg.quiet_hours = { enabled: qhEnabled, start: qhStart, end: qhEnd, days: qhDays };
      }
      const body: AgentConfigInput = {
        scope,
        target_id: scope === "global" ? undefined : targetID,
        config: cfg,
        description,
        enabled,
      };
      return api<AgentConfigEntry>("/v1/admin/agent-config", {
        method: "PUT",
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

  function toggleDay(d: number) {
    setQhDays((cur) => (cur.includes(d) ? cur.filter((x) => x !== d) : [...cur, d].sort()));
  }

  return (
    <Panel>
      <PanelHeader>
        <h3 className="text-sm font-semibold">{initial ? "Edit config" : "New config"}</h3>
      </PanelHeader>
      <PanelBody>
        <form onSubmit={submit} className="space-y-4">
          <div className="grid grid-cols-2 gap-3">
            <Field label="Scope">
              <select
                value={scope}
                disabled={!!initial}
                onChange={(e) => {
                  setScope(e.target.value as AgentConfigEntry["scope"]);
                  setTargetID("");
                }}
                className="w-full rounded-md border border-border bg-panel px-3 py-2 text-sm disabled:opacity-60"
              >
                <option value="global">global (single row)</option>
                <option value="group">group</option>
                <option value="host">host</option>
              </select>
            </Field>
            {scope !== "global" && (
              <Field label="Target">
                <select
                  required
                  value={targetID}
                  onChange={(e) => setTargetID(e.target.value)}
                  className="w-full rounded-md border border-border bg-panel px-3 py-2 text-sm"
                >
                  <option value="">— pick {scope} —</option>
                  {scope === "group"
                    ? groups.map((g) => (
                        <option key={g.id} value={g.id}>{g.name} ({g.member_ids.length})</option>
                      ))
                    : hosts.map((h) => (
                        <option key={h.id} value={h.id}>{h.hostname}</option>
                      ))}
                </select>
              </Field>
            )}
          </div>

          <Field label="Description (optional)">
            <TextInput value={description} onChange={(e) => setDescription(e.target.value)} />
          </Field>

          <fieldset className="rounded-md border border-border p-3">
            <legend className="px-1 text-[11px] font-semibold uppercase tracking-wider text-fg-subtle">Tick rate</legend>
            <div className="grid grid-cols-2 gap-3">
              <Field label="Interval (seconds)" hint="Leave empty to inherit.">
                <TextInput type="number" min={5} max={3600} value={intervalSec} onChange={(e) => setIntervalSec(e.target.value)} />
              </Field>
              <Field label="Spool buffer (MB)" hint="Leave empty to inherit.">
                <TextInput type="number" min={1} max={4096} value={bufferMB} onChange={(e) => setBufferMB(e.target.value)} />
              </Field>
            </div>
          </fieldset>

          <fieldset className="rounded-md border border-border p-3">
            <legend className="px-1 text-[11px] font-semibold uppercase tracking-wider text-fg-subtle">Packages</legend>
            <div className="grid grid-cols-3 gap-3">
              <Field label="Enabled">
                <select
                  value={pkgEnabled}
                  onChange={(e) => setPkgEnabled(e.target.value as "" | "yes" | "no")}
                  className="w-full rounded-md border border-border bg-panel px-3 py-2 text-sm"
                >
                  <option value="">inherit</option>
                  <option value="yes">yes</option>
                  <option value="no">no</option>
                </select>
              </Field>
              <Field label="Update check interval" hint="e.g. 30m, 2h">
                <TextInput value={pkgUpdateInterval} onChange={(e) => setPkgUpdateInterval(e.target.value)} placeholder="30m" />
              </Field>
              <Field label="Full snapshot interval" hint="e.g. 24h">
                <TextInput value={pkgFullSnapshot} onChange={(e) => setPkgFullSnapshot(e.target.value)} placeholder="24h" />
              </Field>
            </div>
          </fieldset>

          <fieldset className="rounded-md border border-border p-3">
            <legend className="px-1 text-[11px] font-semibold uppercase tracking-wider text-fg-subtle">Quiet hours</legend>
            <label className="flex items-center gap-2 text-sm text-fg-muted">
              <input type="checkbox" checked={qhEnabled} onChange={(e) => setQhEnabled(e.target.checked)} />
              Pause ingest during this window
            </label>
            <div className="mt-3 grid grid-cols-2 gap-3">
              <Field label="Start (HH:MM, agent local)">
                <TextInput value={qhStart} onChange={(e) => setQhStart(e.target.value)} placeholder="22:00" />
              </Field>
              <Field label="End (HH:MM)">
                <TextInput value={qhEnd} onChange={(e) => setQhEnd(e.target.value)} placeholder="06:00" />
              </Field>
            </div>
            <div className="mt-3 flex flex-wrap gap-1.5">
              {DAYS.map((d, i) => {
                const active = qhDays.includes(i);
                return (
                  <button
                    key={d}
                    type="button"
                    onClick={() => toggleDay(i)}
                    className={`rounded-md px-2 py-1 text-xs font-mono transition-colors ${
                      active
                        ? "bg-accent/20 text-accent ring-1 ring-inset ring-accent/40"
                        : "bg-panel-2 text-fg-subtle hover:text-fg"
                    }`}
                  >
                    {d}
                  </button>
                );
              })}
              <span className="text-[11px] text-fg-subtle">Empty = every day.</span>
            </div>
          </fieldset>

          <label className="flex items-center gap-2 text-sm text-fg-muted">
            <input type="checkbox" checked={enabled} onChange={(e) => setEnabled(e.target.checked)} />
            Row enabled
          </label>

          {error && <ErrorBox>{error}</ErrorBox>}

          <div className="flex items-center gap-2">
            <Button variant="primary" type="submit" disabled={save.isPending}>
              {save.isPending ? "Saving…" : initial ? "Save" : "Create"}
            </Button>
            <Button type="button" onClick={onCancel}>Cancel</Button>
          </div>
        </form>
      </PanelBody>
    </Panel>
  );
}
