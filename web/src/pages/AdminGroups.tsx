import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { Layers, PencilLine, Plus, Trash2, Users as UsersIcon, X } from "lucide-react";
import { FormEvent, useMemo, useState } from "react";

import {
  Button,
  Empty,
  ErrorBox,
  Field,
  Panel,
  PanelBody,
  PanelHeader,
  Skeleton,
  TBody,
  TD,
  TH,
  THead,
  Table,
  TabItem,
  Tabs,
  TextInput,
} from "../components/ui";
import { api, ApiError } from "../lib/api";
import { Host, HostGroup } from "../lib/types";
import { hostDisplay } from "../lib/utils";

type TabKey = "list" | "create";

const TABS: ReadonlyArray<TabItem<TabKey>> = [
  { key: "list", label: "Groups", icon: Layers },
  { key: "create", label: "Create new", icon: Plus },
];

export function AdminGroups() {
  const qc = useQueryClient();
  const groups = useQuery({
    queryKey: ["groups"],
    queryFn: () => api<{ groups: HostGroup[] }>("/v1/groups"),
  });
  const hosts = useQuery({
    queryKey: ["hosts"],
    queryFn: () => api<{ hosts: Host[] }>("/v1/hosts"),
  });

  const [tab, setTab] = useState<TabKey>("list");
  const [editing, setEditing] = useState<HostGroup | null>(null);
  const [members, setMembers] = useState<HostGroup | null>(null);

  return (
    <div className="mx-auto max-w-5xl space-y-5 p-6">
      <header>
        <h2 className="text-lg font-semibold tracking-tight">Host groups</h2>
        <p className="text-sm text-fg-muted">
          Group hosts by purpose (e.g. <code className="font-mono">prod-web</code>) and reference them from monitors.
        </p>
      </header>

      <Tabs items={TABS} value={tab} onChange={setTab} />

      {tab === "list" ? (
        <>
          {editing && (
            <GroupForm
              initial={editing}
              onCancel={() => setEditing(null)}
              onSaved={() => {
                qc.invalidateQueries({ queryKey: ["groups"] });
                setEditing(null);
              }}
            />
          )}

          {members && (
            <MembersPanel
              group={members}
              allHosts={hosts.data?.hosts ?? []}
              onClose={() => setMembers(null)}
              onSaved={() => {
                qc.invalidateQueries({ queryKey: ["groups"] });
              }}
            />
          )}

          <Panel>
            <PanelHeader>
              <h3 className="text-sm font-semibold">All groups</h3>
            </PanelHeader>
            <PanelBody className="p-0 overflow-x-auto">
              {groups.isLoading ? (
                <div className="p-5">
                  <Skeleton className="h-32" />
                </div>
              ) : (groups.data?.groups ?? []).length === 0 ? (
                <Empty>No groups yet.</Empty>
              ) : (
                <Table>
                  <THead>
                    <tr>
                      <TH>Name</TH>
                      <TH>Description</TH>
                      <TH>Members</TH>
                      <TH className="text-right">Actions</TH>
                    </tr>
                  </THead>
                  <TBody>
                    {(groups.data?.groups ?? []).map((g) => (
                      <tr key={g.id} className="hover:bg-panel-2">
                        <TD className="font-medium">{g.name}</TD>
                        <TD className="text-fg-muted">{g.description || "—"}</TD>
                        <TD className="tabular-nums text-fg-muted">{g.member_ids.length}</TD>
                        <TD className="text-right">
                          <div className="inline-flex items-center gap-1">
                            <Button onClick={() => setMembers(g)}>
                              <UsersIcon className="h-3.5 w-3.5" /> Members
                            </Button>
                            <Button onClick={() => setEditing(g)}>
                              <PencilLine className="h-3.5 w-3.5" /> Edit
                            </Button>
                            <Button
                              variant="danger"
                              onClick={() => {
                                if (confirm(`Delete group "${g.name}"?`))
                                  api(`/v1/groups/${g.id}`, { method: "DELETE" }).then(() =>
                                    qc.invalidateQueries({ queryKey: ["groups"] }),
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
        </>
      ) : (
        <CreateGroupPanel
          allHosts={hosts.data?.hosts ?? []}
          hostsLoading={hosts.isLoading}
          onCreated={() => {
            qc.invalidateQueries({ queryKey: ["groups"] });
            setTab("list");
          }}
        />
      )}
    </div>
  );
}

function GroupForm({
  initial,
  onCancel,
  onSaved,
}: {
  initial: HostGroup | null;
  onCancel: () => void;
  onSaved: () => void;
}) {
  const [name, setName] = useState(initial?.name ?? "");
  const [description, setDescription] = useState(initial?.description ?? "");
  const [error, setError] = useState<string | null>(null);

  const save = useMutation({
    mutationFn: () => {
      const body = { name, description };
      if (initial) {
        return api(`/v1/groups/${initial.id}`, { method: "PUT", body: JSON.stringify(body) });
      }
      return api("/v1/groups", { method: "POST", body: JSON.stringify(body) });
    },
    onSuccess: onSaved,
    onError: (err) => setError(err instanceof ApiError ? err.detail : "failed"),
  });

  function submit(e: FormEvent) {
    e.preventDefault();
    setError(null);
    save.mutate();
  }

  return (
    <Panel>
      <PanelHeader>
        <h3 className="text-sm font-semibold">{initial ? `Edit ${initial.name}` : "New group"}</h3>
      </PanelHeader>
      <PanelBody>
        <form onSubmit={submit} className="space-y-3">
          <Field label="Name">
            <TextInput required value={name} onChange={(e) => setName(e.target.value)} />
          </Field>
          <Field label="Description (optional)">
            <TextInput value={description} onChange={(e) => setDescription(e.target.value)} />
          </Field>
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

function MembersPanel({
  group,
  allHosts,
  onClose,
  onSaved,
}: {
  group: HostGroup;
  allHosts: Host[];
  onClose: () => void;
  onSaved: () => void;
}) {
  const [selected, setSelected] = useState<Set<string>>(new Set(group.member_ids));
  const save = useMutation({
    mutationFn: () =>
      api(`/v1/groups/${group.id}/members`, {
        method: "PUT",
        body: JSON.stringify({ host_ids: Array.from(selected) }),
      }),
    onSuccess: () => {
      onSaved();
      onClose();
    },
  });

  function toggle(id: string) {
    const next = new Set(selected);
    if (next.has(id)) next.delete(id);
    else next.add(id);
    setSelected(next);
  }

  return (
    <Panel>
      <PanelHeader>
        <div>
          <h3 className="text-sm font-semibold">{group.name} — members</h3>
          <p className="text-xs text-fg-subtle">{selected.size} of {allHosts.length} hosts selected</p>
        </div>
        <div className="flex items-center gap-2">
          <Button variant="primary" onClick={() => save.mutate()} disabled={save.isPending}>
            {save.isPending ? "Saving…" : "Save"}
          </Button>
          <Button onClick={onClose}>Cancel</Button>
        </div>
      </PanelHeader>
      <PanelBody className="p-0 overflow-x-auto">
        <Table>
          <THead>
            <tr>
              <TH></TH>
              <TH>Host</TH>
              <TH>Distro</TH>
              <TH>Tags</TH>
            </tr>
          </THead>
          <TBody>
            {allHosts.map((h) => {
              const checked = selected.has(h.id);
              return (
                <tr key={h.id} className="cursor-pointer hover:bg-panel-2" onClick={() => toggle(h.id)}>
                  <TD>
                    <input type="checkbox" checked={checked} onChange={() => toggle(h.id)} />
                  </TD>
                  <TD className="font-medium">{hostDisplay(h)}</TD>
                  <TD className="text-fg-muted">{h.distro || "—"}</TD>
                  <TD className="text-fg-muted font-mono text-xs">{h.tags.join(", ") || "—"}</TD>
                </tr>
              );
            })}
          </TBody>
        </Table>
      </PanelBody>
    </Panel>
  );
}

// CreateGroupPanel is the "Create new" tab body. It posts the group, then
// follows up with a member-assignment PUT once the new id is known, so the
// new group lands with its initial roster in one user flow. Host selection
// is driven by an inline tag filter — any/all match modes plus a free-form
// search keep the picker usable even on fleets with hundreds of hosts.
function CreateGroupPanel({
  allHosts,
  hostsLoading,
  onCreated,
}: {
  allHosts: Host[];
  hostsLoading: boolean;
  onCreated: () => void;
}) {
  const [name, setName] = useState("");
  const [description, setDescription] = useState("");
  const [search, setSearch] = useState("");
  const [activeTags, setActiveTags] = useState<Set<string>>(new Set());
  const [tagMatch, setTagMatch] = useState<"any" | "all">("any");
  const [selected, setSelected] = useState<Set<string>>(new Set());
  const [error, setError] = useState<string | null>(null);

  // Distinct tag set across all hosts. Sorted for stable rendering — without
  // this the chip row would shuffle every refetch because Set iteration order
  // tracks insertion order from the underlying host list.
  const allTags = useMemo(() => {
    const s = new Set<string>();
    for (const h of allHosts) for (const t of h.tags) s.add(t);
    return Array.from(s).sort();
  }, [allHosts]);

  // Hosts visible after applying tag chips + free-text filter. The text match
  // is intentionally permissive (hostname or any tag substring) so the
  // operator can narrow by either dimension without switching inputs.
  const visibleHosts = useMemo(() => {
    const q = search.trim().toLowerCase();
    return allHosts.filter((h) => {
      if (activeTags.size > 0) {
        const has = (t: string) => h.tags.includes(t);
        const ok = tagMatch === "all"
          ? Array.from(activeTags).every(has)
          : Array.from(activeTags).some(has);
        if (!ok) return false;
      }
      if (q) {
        const hay = `${hostDisplay(h)} ${h.tags.join(" ")}`.toLowerCase();
        if (!hay.includes(q)) return false;
      }
      return true;
    });
  }, [allHosts, activeTags, tagMatch, search]);

  const visibleSelected = visibleHosts.filter((h) => selected.has(h.id)).length;

  function toggleTag(t: string) {
    const next = new Set(activeTags);
    if (next.has(t)) next.delete(t);
    else next.add(t);
    setActiveTags(next);
  }

  function toggleHost(id: string) {
    const next = new Set(selected);
    if (next.has(id)) next.delete(id);
    else next.add(id);
    setSelected(next);
  }

  function selectAllVisible() {
    const next = new Set(selected);
    for (const h of visibleHosts) next.add(h.id);
    setSelected(next);
  }

  function clearVisible() {
    const next = new Set(selected);
    for (const h of visibleHosts) next.delete(h.id);
    setSelected(next);
  }

  // Two-step create: POST the group, then PUT members if any were picked.
  // The members call is best-effort relative to the group itself — if it
  // fails the group still exists, but we surface the error so the operator
  // can retry from the Members button on the list tab.
  const save = useMutation({
    mutationFn: async () => {
      const created = await api<HostGroup>("/v1/groups", {
        method: "POST",
        body: JSON.stringify({ name, description }),
      });
      if (selected.size > 0) {
        await api(`/v1/groups/${created.id}/members`, {
          method: "PUT",
          body: JSON.stringify({ host_ids: Array.from(selected) }),
        });
      }
      return created;
    },
    onSuccess: () => {
      setName("");
      setDescription("");
      setSearch("");
      setActiveTags(new Set());
      setSelected(new Set());
      onCreated();
    },
    onError: (err) => setError(err instanceof ApiError ? err.detail : "failed"),
  });

  function submit(e: FormEvent) {
    e.preventDefault();
    setError(null);
    save.mutate();
  }

  return (
    <Panel>
      <PanelHeader>
        <h3 className="text-sm font-semibold">New group</h3>
        <p className="text-xs text-fg-subtle tabular-nums">
          {selected.size} host{selected.size === 1 ? "" : "s"} selected
        </p>
      </PanelHeader>
      <PanelBody>
        <form onSubmit={submit} className="space-y-5">
          <div className="grid gap-3 md:grid-cols-2">
            <Field label="Name">
              <TextInput
                required
                placeholder="prod-web"
                value={name}
                onChange={(e) => setName(e.target.value)}
              />
            </Field>
            <Field label="Description (optional)">
              <TextInput
                placeholder="Production web tier"
                value={description}
                onChange={(e) => setDescription(e.target.value)}
              />
            </Field>
          </div>

          <div className="space-y-2">
            <div className="flex flex-wrap items-center justify-between gap-2">
              <span className="text-xs font-medium text-fg-muted">Filter hosts by tag</span>
              <div className="inline-flex rounded-md border border-border bg-panel p-0.5 text-[11px]">
                {(["any", "all"] as const).map((m) => (
                  <button
                    key={m}
                    type="button"
                    onClick={() => setTagMatch(m)}
                    className={`rounded px-2 py-0.5 font-medium transition-colors duration-150 ${
                      tagMatch === m
                        ? "bg-panel-2 text-fg shadow-panel"
                        : "text-fg-subtle hover:text-fg"
                    }`}
                  >
                    match {m}
                  </button>
                ))}
              </div>
            </div>
            {allTags.length === 0 ? (
              <p className="text-xs text-fg-subtle">No tags defined on any host yet.</p>
            ) : (
              <div className="flex flex-wrap gap-1.5">
                {allTags.map((t) => {
                  const on = activeTags.has(t);
                  return (
                    <button
                      key={t}
                      type="button"
                      onClick={() => toggleTag(t)}
                      className={`inline-flex items-center gap-1 rounded-full border px-2 py-0.5 font-mono text-[11px] transition-colors duration-150 ${
                        on
                          ? "border-accent/40 bg-accent/10 text-accent"
                          : "border-border bg-panel-2 text-fg-muted hover:border-border-strong hover:text-fg"
                      }`}
                    >
                      {t}
                      {on && <X className="h-3 w-3" />}
                    </button>
                  );
                })}
              </div>
            )}
          </div>

          <div className="space-y-2">
            <div className="flex flex-wrap items-center justify-between gap-2">
              <span className="text-xs font-medium text-fg-muted">
                Members ({visibleSelected} of {visibleHosts.length} visible selected)
              </span>
              <div className="flex items-center gap-2">
                <TextInput
                  className="w-44"
                  placeholder="Search hostname or tag…"
                  value={search}
                  onChange={(e) => setSearch(e.target.value)}
                />
                <Button type="button" size="sm" onClick={selectAllVisible} disabled={visibleHosts.length === 0}>
                  Select visible
                </Button>
                <Button type="button" size="sm" onClick={clearVisible} disabled={visibleSelected === 0}>
                  Clear visible
                </Button>
              </div>
            </div>
            <div className="overflow-x-auto rounded-md border border-border">
              {hostsLoading ? (
                <div className="p-5">
                  <Skeleton className="h-32" />
                </div>
              ) : visibleHosts.length === 0 ? (
                <Empty>No hosts match the current filter.</Empty>
              ) : (
                <Table>
                  <THead>
                    <tr>
                      <TH></TH>
                      <TH>Host</TH>
                      <TH>Distro</TH>
                      <TH>Tags</TH>
                    </tr>
                  </THead>
                  <TBody>
                    {visibleHosts.map((h) => {
                      const checked = selected.has(h.id);
                      return (
                        <tr key={h.id} className="cursor-pointer hover:bg-panel-2" onClick={() => toggleHost(h.id)}>
                          <TD>
                            <input type="checkbox" checked={checked} onChange={() => toggleHost(h.id)} />
                          </TD>
                          <TD className="font-medium">{hostDisplay(h)}</TD>
                          <TD className="text-fg-muted">{h.distro || "—"}</TD>
                          <TD className="text-fg-muted font-mono text-xs">{h.tags.join(", ") || "—"}</TD>
                        </tr>
                      );
                    })}
                  </TBody>
                </Table>
              )}
            </div>
          </div>

          {error && <ErrorBox>{error}</ErrorBox>}

          <div className="flex items-center gap-2">
            <Button variant="primary" type="submit" disabled={save.isPending || !name.trim()}>
              {save.isPending ? "Creating…" : "Create group"}
            </Button>
          </div>
        </form>
      </PanelBody>
    </Panel>
  );
}
