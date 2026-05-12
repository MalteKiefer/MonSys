import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { Eye, Layers, PencilLine, Plus, Server, Settings, Trash2, Users } from "lucide-react";
import { FormEvent, useMemo, useState } from "react";

import { Page } from "../components/page";
import {
  Button,
  Empty,
  ErrorBox,
  Field,
  Panel,
  PanelBody,
  PanelHeader,
  Skeleton,
  StatusPill,
  TabItem,
  Tabs,
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
import { hostDisplay } from "../lib/utils";

const DAYS = ["Sun", "Mon", "Tue", "Wed", "Thu", "Fri", "Sat"];

// Tabs split by scope axis. The original page mixed global/group/host rows in
// a single table with a Scope column; that conflated the singleton "global
// defaults" row with the N-of-each group/host overrides. Separating by tab:
//   - makes the singleton nature of `global` explicit (one row, edited in
//     place);
//   - lets the group/host tables drop the redundant Scope column;
//   - gives the merged-config preview its own surface instead of competing
//     with the list for vertical space.
type TabKey = "global" | "groups" | "hosts" | "preview";

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

  const [tab, setTab] = useState<TabKey>("global");
  const [editing, setEditing] = useState<AgentConfigEntry | null>(null);
  const [creating, setCreating] = useState<AgentConfigEntry["scope"] | null>(null);
  const [selectedHostID, setSelectedHostID] = useState<string>("");
  const [previewHost, setPreviewHost] = useState<Host | null>(null);

  const configs = list.data?.configs ?? [];
  const globalCfg = useMemo(() => configs.find((c) => c.scope === "global") ?? null, [configs]);
  const groupCfgs = useMemo(() => configs.filter((c) => c.scope === "group"), [configs]);
  const hostCfgs = useMemo(() => configs.filter((c) => c.scope === "host"), [configs]);

  const tabs: ReadonlyArray<TabItem<TabKey>> = [
    { key: "global", label: "Global defaults", icon: Settings, badge: globalCfg ? "1" : "0" },
    { key: "groups", label: "Group overrides", icon: Users, badge: String(groupCfgs.length) },
    { key: "hosts", label: "Host overrides", icon: Server, badge: String(hostCfgs.length) },
    { key: "preview", label: "Preview", icon: Eye },
  ];

  const closeForm = () => {
    setEditing(null);
    setCreating(null);
  };
  const onSaved = () => {
    qc.invalidateQueries({ queryKey: ["agent-configs"] });
    closeForm();
  };

  // Show form when creating a new row or editing an existing one. The form's
  // `scope` is locked when editing (server keys rows by (scope, target_id));
  // when creating we seed it from the tab the user pressed "New" on so they
  // don't have to reselect.
  const formInitial: AgentConfigEntry | null = editing;
  const formScope: AgentConfigEntry["scope"] = editing?.scope ?? creating ?? "global";

  return (
    <Page
      title="Agent configuration"
      subtitle="Server-managed knobs the agent applies after first start. Host overrides take precedence over group overrides, group overrides over global. Missing keys fall back to the agent's compiled defaults."
    >
      <Tabs items={tabs} value={tab} onChange={setTab} />

      {(creating !== null || editing) && (
        <ConfigForm
          initial={formInitial}
          scope={formScope}
          lockScope={editing !== null || creating !== null}
          hosts={hosts.data?.hosts ?? []}
          groups={groups.data?.groups ?? []}
          onCancel={closeForm}
          onSaved={onSaved}
        />
      )}

      {tab === "global" && (
        <GlobalTab
          cfg={globalCfg}
          loading={list.isLoading}
          onEdit={() => globalCfg && setEditing(globalCfg)}
          onCreate={() => setCreating("global")}
          onDelete={() => {
            if (!globalCfg) return;
            if (confirm("Delete the global defaults row? Agent will fall back to compiled defaults until a new one is created."))
              api(`/v1/admin/agent-config/${globalCfg.id}`, { method: "DELETE" }).then(() =>
                qc.invalidateQueries({ queryKey: ["agent-configs"] }),
              );
          }}
        />
      )}

      {tab === "groups" && (
        <ScopeTable
          scope="group"
          rows={groupCfgs}
          loading={list.isLoading}
          onNew={() => setCreating("group")}
          onEdit={(c) => setEditing(c)}
          onDelete={(c) => {
            if (confirm(`Delete group override (${c.target_name ?? c.target_id?.slice(0, 8)})?`))
              api(`/v1/admin/agent-config/${c.id}`, { method: "DELETE" }).then(() =>
                qc.invalidateQueries({ queryKey: ["agent-configs"] }),
              );
          }}
        />
      )}

      {tab === "hosts" && (
        <ScopeTable
          scope="host"
          rows={hostCfgs}
          loading={list.isLoading}
          onNew={() => setCreating("host")}
          onEdit={(c) => setEditing(c)}
          onDelete={(c) => {
            if (confirm(`Delete host override (${c.target_name ?? c.target_id?.slice(0, 8)})?`))
              api(`/v1/admin/agent-config/${c.id}`, { method: "DELETE" }).then(() =>
                qc.invalidateQueries({ queryKey: ["agent-configs"] }),
              );
          }}
        />
      )}

      {tab === "preview" && (
        <>
          <Panel>
            <PanelHeader>
              <div className="flex items-center gap-2">
                <Eye className="h-4 w-4 text-fg-muted" />
                <h3 className="text-sm font-semibold">Resolve merged config</h3>
              </div>
              <div className="flex items-center gap-2">
                <select
                  value={selectedHostID}
                  onChange={(e) => setSelectedHostID(e.target.value)}
                  className="rounded-md border border-border bg-panel px-2 py-1 text-xs"
                >
                  <option value="">Pick a host…</option>
                  {(hosts.data?.hosts ?? []).map((h) => (
                    <option key={h.id} value={h.id}>{hostDisplay(h)}</option>
                  ))}
                </select>
                <Button
                  variant="primary"
                  disabled={!selectedHostID}
                  onClick={() => {
                    const h = (hosts.data?.hosts ?? []).find((x) => x.id === selectedHostID);
                    if (h) setPreviewHost(h);
                  }}
                >
                  <Eye className="h-3.5 w-3.5" /> Preview
                </Button>
              </div>
            </PanelHeader>
            <PanelBody>
              <p className="text-xs text-fg-subtle">
                Pick a host to see the exact config the agent will receive after merging the global defaults, any
                matching group overrides, and the host-specific override (if any). Each leaf is tagged with the
                layer that supplied it.
              </p>
            </PanelBody>
          </Panel>
          {previewHost && (
            <PreviewPanel
              host={previewHost}
              allConfigs={configs}
              onClose={() => setPreviewHost(null)}
            />
          )}
        </>
      )}
    </Page>
  );
}

// ---- Per-tab views --------------------------------------------------------

function GlobalTab({
  cfg,
  loading,
  onEdit,
  onCreate,
  onDelete,
}: {
  cfg: AgentConfigEntry | null;
  loading: boolean;
  onEdit: () => void;
  onCreate: () => void;
  onDelete: () => void;
}) {
  return (
    <Panel>
      <PanelHeader>
        <div className="flex items-center gap-2">
          <Settings className="h-4 w-4 text-fg-muted" />
          <h3 className="text-sm font-semibold">Global defaults</h3>
          <span className="text-[11px] text-fg-subtle">— singleton row applied to every agent before group/host overrides</span>
        </div>
        <div className="flex items-center gap-2">
          {cfg ? (
            <>
              <Button onClick={onEdit}>
                <PencilLine className="h-3.5 w-3.5" /> Edit
              </Button>
              <Button variant="danger" onClick={onDelete}>
                <Trash2 className="h-3.5 w-3.5" />
              </Button>
            </>
          ) : (
            <Button variant="primary" onClick={onCreate}>
              <Plus className="h-3.5 w-3.5" /> Create
            </Button>
          )}
        </div>
      </PanelHeader>
      <PanelBody>
        {loading ? (
          <Skeleton className="h-32" />
        ) : !cfg ? (
          <Empty>No global defaults set. The agent falls back to its compiled defaults.</Empty>
        ) : (
          <div className="space-y-3">
            <div className="flex flex-wrap items-center gap-2 text-xs">
              <StatusPill status={cfg.enabled ? "ok" : "offline"}>{cfg.enabled ? "enabled" : "disabled"}</StatusPill>
              {cfg.description && <span className="text-fg-muted">{cfg.description}</span>}
            </div>
            <pre className="overflow-auto rounded-md border border-border bg-bg p-3 font-mono text-[11px] leading-relaxed text-fg">
{JSON.stringify(cfg.config, null, 2)}
            </pre>
          </div>
        )}
      </PanelBody>
    </Panel>
  );
}

function ScopeTable({
  scope,
  rows,
  loading,
  onNew,
  onEdit,
  onDelete,
}: {
  scope: "group" | "host";
  rows: AgentConfigEntry[];
  loading: boolean;
  onNew: () => void;
  onEdit: (c: AgentConfigEntry) => void;
  onDelete: (c: AgentConfigEntry) => void;
}) {
  const Icon = scope === "group" ? Users : Server;
  const title = scope === "group" ? "Group overrides" : "Host overrides";
  const subtitle =
    scope === "group"
      ? "Applied to every host in the group; merged after globals, before host overrides."
      : "Applied to a single host; merged last, wins all collisions.";
  return (
    <Panel>
      <PanelHeader>
        <div className="flex items-center gap-2">
          <Icon className="h-4 w-4 text-fg-muted" />
          <h3 className="text-sm font-semibold">{title}</h3>
          <span className="text-[11px] text-fg-subtle">— {subtitle}</span>
        </div>
        <Button variant="primary" onClick={onNew}>
          <Plus className="h-3.5 w-3.5" /> New {scope}
        </Button>
      </PanelHeader>
      <PanelBody className="p-0 overflow-x-auto">
        {loading ? (
          <div className="p-5">
            <Skeleton className="h-32" />
          </div>
        ) : rows.length === 0 ? (
          <Empty>No {scope} overrides yet.</Empty>
        ) : (
          <Table>
            <THead>
              <tr>
                <TH>{scope === "group" ? "Group" : "Host"}</TH>
                <TH>Interval</TH>
                <TH>Quiet hours</TH>
                <TH>Description</TH>
                <TH>Enabled</TH>
                <TH className="text-right">Actions</TH>
              </tr>
            </THead>
            <TBody>
              {rows.map((c) => (
                <tr key={c.id} className="hover:bg-panel-2">
                  <TD className="font-mono text-xs">
                    {c.target_name || c.target_id?.slice(0, 8)}
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
                      <Button onClick={() => onEdit(c)}>
                        <PencilLine className="h-3.5 w-3.5" /> Edit
                      </Button>
                      <Button variant="danger" onClick={() => onDelete(c)}>
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
  );
}

// ---- Resolved config preview / diff view ----------------------------------

type Origin = "default" | "global" | "group" | "host";

// Walk the resolved config and tag each leaf field with the origin layer that
// supplied it. The server returns the merge result + the ordered list of
// scopes that contributed; we walk the per-scope drafts (computed from the
// list query) in the same order to attribute each leaf to its winning layer.
// Pure JSON tree-view — no diff library.

type LeafValue = string | number | boolean | null;

// Flatten a config object into a map of dotted keypath → leaf value. Arrays
// are stringified as JSON because the agent treats `days` and `schedules` as
// opaque list overrides (the engine replaces, not merges).
function flatten(obj: unknown, prefix = "", out: Record<string, LeafValue> = {}): Record<string, LeafValue> {
  if (obj === null || obj === undefined) return out;
  if (Array.isArray(obj)) {
    out[prefix] = JSON.stringify(obj);
    return out;
  }
  if (typeof obj !== "object") {
    out[prefix] = obj as LeafValue;
    return out;
  }
  for (const [k, v] of Object.entries(obj as Record<string, unknown>)) {
    const key = prefix ? `${prefix}.${k}` : k;
    if (v !== null && typeof v === "object" && !Array.isArray(v)) {
      flatten(v, key, out);
    } else if (Array.isArray(v)) {
      out[key] = JSON.stringify(v);
    } else {
      out[key] = (v ?? null) as LeafValue;
    }
  }
  return out;
}

// For each leaf in `final`, find which layer (global/group/host) supplied the
// winning value by scanning layers from most specific to least specific. If
// no layer has the key, attribute to "default" (agent's compiled fallback).
function attributeOrigins(
  final: AgentConfig,
  layers: { scope: Origin; cfg: AgentConfig }[],
): Record<string, Origin> {
  const flat = flatten(final);
  const layerFlat = layers.map((l) => ({ scope: l.scope, flat: flatten(l.cfg) }));
  const origin: Record<string, Origin> = {};
  for (const [key, finalVal] of Object.entries(flat)) {
    let attributed: Origin = "default";
    // Most-specific first: host wins, then group, then global. We compare
    // string-encoded values so arrays/objects collapse identically.
    for (const layer of [...layerFlat].reverse()) {
      if (key in layer.flat && String(layer.flat[key]) === String(finalVal)) {
        attributed = layer.scope;
        break;
      }
    }
    origin[key] = attributed;
  }
  return origin;
}

function originLabel(o: Origin): string {
  return o;
}

function originPillStatus(o: Origin): "info" | "warn" | "ok" | "offline" {
  if (o === "global") return "info";
  if (o === "group") return "warn";
  if (o === "host") return "ok";
  return "offline";
}

// Render a JSON tree with per-leaf origin tags. Host-sourced leaves get a
// `text-accent` highlight per spec.
function JsonTree({
  value,
  origins,
  path = "",
}: {
  value: unknown;
  origins: Record<string, Origin>;
  path?: string;
}) {
  if (value === null || value === undefined) {
    return <span className="text-fg-subtle">null</span>;
  }
  if (Array.isArray(value)) {
    const o = origins[path] ?? "default";
    const cls = o === "host" ? "text-accent" : "text-fg";
    return (
      <span className={`font-mono ${cls}`}>{JSON.stringify(value)}</span>
    );
  }
  if (typeof value !== "object") {
    const o = origins[path] ?? "default";
    const cls = o === "host" ? "text-accent" : "text-fg";
    return (
      <span className={`font-mono ${cls}`}>{JSON.stringify(value)}</span>
    );
  }
  const entries = Object.entries(value as Record<string, unknown>);
  if (entries.length === 0) {
    return <span className="text-fg-subtle">{"{}"}</span>;
  }
  return (
    <div className="space-y-0.5">
      {entries.map(([k, v]) => {
        const childPath = path ? `${path}.${k}` : k;
        const isObj = v !== null && typeof v === "object" && !Array.isArray(v);
        // Find the deepest origin attached to anything inside this subtree;
        // for object nodes we surface the children's origins individually so
        // the parent label is "—". For leaves and arrays we look up directly.
        const leafOrigin: Origin | null = isObj ? null : origins[childPath] ?? "default";
        const keyCls = leafOrigin === "host" ? "text-accent" : "text-fg-muted";
        return (
          <div key={k} className="flex items-baseline gap-2 text-[12px] leading-relaxed">
            <span className={`font-mono ${keyCls}`}>{k}:</span>
            {isObj ? (
              <div className="border-l border-border pl-3">
                <JsonTree value={v} origins={origins} path={childPath} />
              </div>
            ) : (
              <>
                <JsonTree value={v} origins={origins} path={childPath} />
                {leafOrigin && leafOrigin !== "default" && (
                  <span
                    className={`ml-1 rounded px-1 py-px text-[9px] font-mono uppercase tracking-wide ring-1 ring-inset ${
                      leafOrigin === "host"
                        ? "bg-accent/15 text-accent ring-accent/30"
                        : leafOrigin === "group"
                          ? "bg-warn/10 text-warn ring-warn/30"
                          : "bg-info/10 text-info ring-info/30"
                    }`}
                  >
                    {leafOrigin}
                  </span>
                )}
              </>
            )}
          </div>
        );
      })}
    </div>
  );
}

function PreviewPanel({
  host,
  allConfigs,
  onClose,
}: {
  host: Host;
  allConfigs: AgentConfigEntry[];
  onClose: () => void;
}) {
  const preview = useQuery({
    queryKey: ["agent-config-preview", host.id],
    queryFn: () => api<AgentConfigResolved>(`/v1/admin/agent-config/preview/${host.id}`),
  });

  // Source layers, ordered global → group(s) → host. Group rows are kept in
  // the order the host's group memberships are listed; the server applies
  // them in deterministic order so any ordering difference would surface as
  // a mismatched origin pill, which is itself a useful signal.
  const layers = useMemo(() => {
    const enabled = allConfigs.filter((c) => c.enabled);
    const globalCfg = enabled.find((c) => c.scope === "global");
    const hostCfg = enabled.find((c) => c.scope === "host" && c.target_id === host.id);
    const groupIDs = new Set(host.groups.map((g) => g.id));
    const groupCfgs = enabled.filter(
      (c) => c.scope === "group" && c.target_id && groupIDs.has(c.target_id),
    );
    const list: {
      scope: Origin;
      label: string;
      entry?: AgentConfigEntry;
      cfg: AgentConfig;
    }[] = [];
    if (globalCfg) list.push({ scope: "global", label: "global", entry: globalCfg, cfg: globalCfg.config });
    for (const g of groupCfgs) {
      list.push({
        scope: "group",
        label: `group · ${g.target_name ?? g.target_id?.slice(0, 8) ?? ""}`,
        entry: g,
        cfg: g.config,
      });
    }
    if (hostCfg) list.push({ scope: "host", label: `host · ${hostDisplay(host)}`, entry: hostCfg, cfg: hostCfg.config });
    return list;
  }, [allConfigs, host]);

  const origins = useMemo(() => {
    const cfg = preview.data?.config ?? {};
    return attributeOrigins(cfg, layers.map((l) => ({ scope: l.scope, cfg: l.cfg })));
  }, [preview.data, layers]);

  return (
    <Panel>
      <PanelHeader>
        <div className="flex items-center gap-2">
          <Eye className="h-4 w-4 text-fg-muted" />
          <h3 className="text-sm font-semibold">Resolved config preview · {hostDisplay(host)}</h3>
        </div>
        <Button onClick={onClose}>Close</Button>
      </PanelHeader>
      <PanelBody>
        {preview.isLoading ? (
          <Skeleton className="h-48" />
        ) : preview.error ? (
          <ErrorBox>{(preview.error as Error).message}</ErrorBox>
        ) : (
          <>
            <p className="mb-3 text-xs text-fg-subtle">
              Sources merged:{" "}
              <span className="font-mono text-fg-muted">
                {preview.data?.source_scopes.join(" → ") || "(defaults only)"}
              </span>
            </p>

            <div className="grid grid-cols-1 gap-4 lg:grid-cols-2">
              {/* Left — source layers */}
              <section className="space-y-3">
                <h4 className="flex items-center gap-1.5 text-[11px] font-semibold uppercase tracking-wider text-fg-subtle">
                  <Layers className="h-3 w-3" /> Source layers
                </h4>
                {layers.length === 0 ? (
                  <p className="rounded-md border border-dashed border-border p-3 text-xs text-fg-subtle">
                    No matching layers — agent uses compiled defaults.
                  </p>
                ) : (
                  layers.map((layer, i) => (
                    <div
                      key={i}
                      className="rounded-md border border-border bg-panel-2 p-3"
                    >
                      <div className="mb-2 flex items-center gap-2 text-xs">
                        <StatusPill status={originPillStatus(layer.scope)}>
                          {originLabel(layer.scope)}
                        </StatusPill>
                        <span className="font-mono text-fg-muted">{layer.label}</span>
                        {layer.entry?.description && (
                          <span className="truncate text-fg-subtle">— {layer.entry.description}</span>
                        )}
                      </div>
                      <pre className="overflow-auto rounded-md border border-border bg-bg p-2 font-mono text-[11px] leading-relaxed text-fg">
{JSON.stringify(layer.cfg, null, 2)}
                      </pre>
                    </div>
                  ))
                )}
              </section>

              {/* Right — final merged */}
              <section className="space-y-3">
                <h4 className="flex items-center gap-1.5 text-[11px] font-semibold uppercase tracking-wider text-fg-subtle">
                  <Eye className="h-3 w-3" /> Final config
                  <span className="ml-auto inline-flex items-center gap-2 text-[10px] font-normal normal-case tracking-normal text-fg-subtle">
                    origin tags:
                    <StatusPill status="info">global</StatusPill>
                    <StatusPill status="warn">group</StatusPill>
                    <StatusPill status="ok">host</StatusPill>
                  </span>
                </h4>
                <div className="rounded-md border border-border bg-bg p-3">
                  {Object.keys(preview.data?.config ?? {}).length === 0 ? (
                    <p className="text-xs text-fg-subtle">
                      No keys set. Agent applies its compiled defaults verbatim.
                    </p>
                  ) : (
                    <JsonTree value={preview.data?.config ?? {}} origins={origins} />
                  )}
                </div>
                <p className="text-[11px] text-fg-subtle">
                  Host-sourced fields are highlighted in <span className="text-accent">accent</span>;
                  group / global tags appear next to each leaf.
                </p>
              </section>
            </div>
          </>
        )}
      </PanelBody>
    </Panel>
  );
}

function ConfigForm({
  initial,
  scope: initialScope,
  lockScope,
  hosts,
  groups,
  onCancel,
  onSaved,
}: {
  initial: AgentConfigEntry | null;
  // Seeded from the active tab when creating; copied from `initial.scope` when
  // editing. The form locks the field whenever `lockScope` is true so the user
  // can't switch a row's scope mid-edit (server keys rows by (scope, target)).
  scope: AgentConfigEntry["scope"];
  lockScope: boolean;
  hosts: Host[];
  groups: HostGroup[];
  onCancel: () => void;
  onSaved: () => void;
}) {
  const [scope, setScope] = useState<AgentConfigEntry["scope"]>(initial?.scope ?? initialScope);
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
      // Upsert semantics: the server exposes a single PUT /v1/admin/agent-config
      // route that creates-or-replaces a row keyed by (scope, target_id). There
      // is intentionally no POST + no PUT-with-id pair here — global is a
      // singleton row, and group/host rows collide on the (scope, target_id)
      // unique index, so the same payload shape works for both new and edit.
      // (See handleUpsertAgentConfig + Store.UpsertAgentConfig.)
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
                disabled={lockScope}
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
                        <option key={h.id} value={h.id}>{hostDisplay(h)}</option>
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
