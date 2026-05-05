import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { PencilLine, Plus, Trash2, Users as UsersIcon } from "lucide-react";
import { FormEvent, useState } from "react";

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
  TextInput,
} from "../components/ui";
import { api, ApiError } from "../lib/api";
import { Host, HostGroup } from "../lib/types";
import { hostDisplay } from "../lib/utils";

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

  const [editing, setEditing] = useState<HostGroup | null>(null);
  const [creating, setCreating] = useState(false);
  const [members, setMembers] = useState<HostGroup | null>(null);

  return (
    <div className="mx-auto max-w-5xl space-y-5 p-6">
      <header>
        <h2 className="text-lg font-semibold tracking-tight">Host groups</h2>
        <p className="text-sm text-fg-muted">
          Group hosts by purpose (e.g. <code className="font-mono">prod-web</code>) and reference them from monitors.
        </p>
      </header>

      {(creating || editing) && (
        <GroupForm
          initial={editing}
          onCancel={() => {
            setEditing(null);
            setCreating(false);
          }}
          onSaved={() => {
            qc.invalidateQueries({ queryKey: ["groups"] });
            setEditing(null);
            setCreating(false);
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
          <Button variant="primary" onClick={() => setCreating(true)}>
            <Plus className="h-3.5 w-3.5" /> New group
          </Button>
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
