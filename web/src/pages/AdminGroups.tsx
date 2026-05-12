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
import { useT } from "../i18n/useT";
import { api, ApiError } from "../lib/api";
import { Host, HostGroup } from "../lib/types";
import { hostDisplay } from "../lib/utils";

type TabKey = "list" | "create";

export function AdminGroups() {
  const { t } = useT(["admin", "common"]);
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

  const tabs: ReadonlyArray<TabItem<TabKey>> = useMemo(
    () => [
      { key: "list", label: t("admin:groups.tabs.list"), icon: Layers },
      { key: "create", label: t("admin:groups.tabs.create"), icon: Plus },
    ],
    [t],
  );

  return (
    <div className="mx-auto max-w-5xl space-y-5 p-6">
      <header>
        <h2 className="text-lg font-semibold tracking-tight">{t("admin:groups.headerTitle")}</h2>
        <p className="text-sm text-fg-muted">
          {t("admin:groups.headerSubtitleLead")}
          <code className="font-mono">{t("admin:groups.headerSubtitleExample")}</code>
          {t("admin:groups.headerSubtitleTail")}
        </p>
      </header>

      <Tabs items={tabs} value={tab} onChange={setTab} />

      {tab === "list" ? (
        <>
          {editing && (
            <GroupForm
              initial={editing}
              onCancel={() => setEditing(null)}
              onSaved={() => {
                void qc.invalidateQueries({ queryKey: ["groups"] });
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
                void qc.invalidateQueries({ queryKey: ["groups"] });
              }}
            />
          )}

          <Panel>
            <PanelHeader>
              <h3 className="text-sm font-semibold">{t("admin:groups.list.title")}</h3>
            </PanelHeader>
            <PanelBody className="p-0 overflow-x-auto">
              {groups.isLoading ? (
                <div className="p-5">
                  <Skeleton className="h-32" />
                </div>
              ) : (groups.data?.groups ?? []).length === 0 ? (
                <Empty>{t("admin:groups.list.empty")}</Empty>
              ) : (
                <Table>
                  <THead>
                    <tr>
                      <TH>{t("admin:groups.list.columns.name")}</TH>
                      <TH>{t("admin:groups.list.columns.description")}</TH>
                      <TH>{t("admin:groups.list.columns.members")}</TH>
                      <TH className="text-right">{t("admin:groups.list.columns.actions")}</TH>
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
                              <UsersIcon className="h-3.5 w-3.5" /> {t("admin:groups.list.membersBtn")}
                            </Button>
                            <Button onClick={() => setEditing(g)}>
                              <PencilLine className="h-3.5 w-3.5" /> {t("admin:groups.list.editBtn")}
                            </Button>
                            <Button
                              variant="danger"
                              onClick={() => {
                                if (confirm(t("admin:groups.list.confirmDelete", { name: g.name })))
                                  void api(`/v1/groups/${g.id}`, { method: "DELETE" }).then(() =>
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
            void qc.invalidateQueries({ queryKey: ["groups"] });
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
  const { t } = useT(["admin", "common"]);
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
    onError: (err) =>
      setError(err instanceof ApiError ? err.detail : t("admin:groups.form.failed")),
  });

  function submit(e: FormEvent) {
    e.preventDefault();
    setError(null);
    save.mutate();
  }

  return (
    <Panel>
      <PanelHeader>
        <h3 className="text-sm font-semibold">
          {initial
            ? t("admin:groups.form.editTitle", { name: initial.name })
            : t("admin:groups.form.newTitle")}
        </h3>
      </PanelHeader>
      <PanelBody>
        <form onSubmit={submit} className="space-y-3">
          <Field label={t("admin:groups.form.name")}>
            <TextInput required value={name} onChange={(e) => setName(e.target.value)} />
          </Field>
          <Field label={t("admin:groups.form.description")}>
            <TextInput value={description} onChange={(e) => setDescription(e.target.value)} />
          </Field>
          {error && <ErrorBox>{error}</ErrorBox>}
          <div className="flex items-center gap-2">
            <Button variant="primary" type="submit" disabled={save.isPending}>
              {save.isPending
                ? t("admin:groups.form.saving")
                : initial
                  ? t("admin:groups.form.save")
                  : t("admin:groups.form.create")}
            </Button>
            <Button type="button" onClick={onCancel}>
              {t("admin:groups.form.cancel")}
            </Button>
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
  const { t } = useT(["admin", "common"]);
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
          <h3 className="text-sm font-semibold">
            {t("admin:groups.members.title", { name: group.name })}
          </h3>
          <p className="text-xs text-fg-subtle">
            {t("admin:groups.members.selectedOf", {
              selected: selected.size,
              total: allHosts.length,
            })}
          </p>
        </div>
        <div className="flex items-center gap-2">
          <Button variant="primary" onClick={() => save.mutate()} disabled={save.isPending}>
            {save.isPending ? t("admin:groups.members.saving") : t("admin:groups.members.save")}
          </Button>
          <Button onClick={onClose}>{t("admin:groups.members.cancel")}</Button>
        </div>
      </PanelHeader>
      <PanelBody className="p-0 overflow-x-auto">
        <Table>
          <THead>
            <tr>
              <TH></TH>
              <TH>{t("admin:groups.members.columns.host")}</TH>
              <TH>{t("admin:groups.members.columns.distro")}</TH>
              <TH>{t("admin:groups.members.columns.tags")}</TH>
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
  const { t } = useT(["admin", "common"]);
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
    for (const h of allHosts) for (const tag of h.tags) s.add(tag);
    return Array.from(s).sort();
  }, [allHosts]);

  // Hosts visible after applying tag chips + free-text filter. The text match
  // is intentionally permissive (hostname or any tag substring) so the
  // operator can narrow by either dimension without switching inputs.
  const visibleHosts = useMemo(() => {
    const q = search.trim().toLowerCase();
    return allHosts.filter((h) => {
      if (activeTags.size > 0) {
        const has = (tg: string) => h.tags.includes(tg);
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

  function toggleTag(tag: string) {
    const next = new Set(activeTags);
    if (next.has(tag)) next.delete(tag);
    else next.add(tag);
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
    onError: (err) =>
      setError(err instanceof ApiError ? err.detail : t("admin:groups.create.failed")),
  });

  function submit(e: FormEvent) {
    e.preventDefault();
    setError(null);
    save.mutate();
  }

  return (
    <Panel>
      <PanelHeader>
        <h3 className="text-sm font-semibold">{t("admin:groups.create.panelTitle")}</h3>
        <p className="text-xs text-fg-subtle tabular-nums">
          {t("admin:groups.create.countSelected", { count: selected.size })}
        </p>
      </PanelHeader>
      <PanelBody>
        <form onSubmit={submit} className="space-y-5">
          <div className="grid gap-3 md:grid-cols-2">
            <Field label={t("admin:groups.form.name")}>
              <TextInput
                required
                placeholder={t("admin:groups.create.namePh")}
                value={name}
                onChange={(e) => setName(e.target.value)}
              />
            </Field>
            <Field label={t("admin:groups.form.description")}>
              <TextInput
                placeholder={t("admin:groups.create.descriptionPh")}
                value={description}
                onChange={(e) => setDescription(e.target.value)}
              />
            </Field>
          </div>

          <div className="space-y-2">
            <div className="flex flex-wrap items-center justify-between gap-2">
              <span className="text-xs font-medium text-fg-muted">
                {t("admin:groups.create.filterByTag")}
              </span>
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
                    {m === "any"
                      ? t("admin:groups.create.matchAny")
                      : t("admin:groups.create.matchAll")}
                  </button>
                ))}
              </div>
            </div>
            {allTags.length === 0 ? (
              <p className="text-xs text-fg-subtle">{t("admin:groups.create.noTags")}</p>
            ) : (
              <div className="flex flex-wrap gap-1.5">
                {allTags.map((tag) => {
                  const on = activeTags.has(tag);
                  return (
                    <button
                      key={tag}
                      type="button"
                      onClick={() => toggleTag(tag)}
                      className={`inline-flex items-center gap-1 rounded-full border px-2 py-0.5 font-mono text-[11px] transition-colors duration-150 ${
                        on
                          ? "border-accent/40 bg-accent/10 text-accent"
                          : "border-border bg-panel-2 text-fg-muted hover:border-border-strong hover:text-fg"
                      }`}
                    >
                      {tag}
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
                {t("admin:groups.create.membersHeader", {
                  visibleSelected,
                  visible: visibleHosts.length,
                })}
              </span>
              <div className="flex items-center gap-2">
                <TextInput
                  className="w-44"
                  placeholder={t("admin:groups.create.searchPh")}
                  value={search}
                  onChange={(e) => setSearch(e.target.value)}
                />
                <Button type="button" size="sm" onClick={selectAllVisible} disabled={visibleHosts.length === 0}>
                  {t("admin:groups.create.selectVisible")}
                </Button>
                <Button type="button" size="sm" onClick={clearVisible} disabled={visibleSelected === 0}>
                  {t("admin:groups.create.clearVisible")}
                </Button>
              </div>
            </div>
            <div className="overflow-x-auto rounded-md border border-border">
              {hostsLoading ? (
                <div className="p-5">
                  <Skeleton className="h-32" />
                </div>
              ) : visibleHosts.length === 0 ? (
                <Empty>{t("admin:groups.create.noMatches")}</Empty>
              ) : (
                <Table>
                  <THead>
                    <tr>
                      <TH></TH>
                      <TH>{t("admin:groups.create.columns.host")}</TH>
                      <TH>{t("admin:groups.create.columns.distro")}</TH>
                      <TH>{t("admin:groups.create.columns.tags")}</TH>
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
              {save.isPending
                ? t("admin:groups.create.submitting")
                : t("admin:groups.create.submit")}
            </Button>
          </div>
        </form>
      </PanelBody>
    </Panel>
  );
}
