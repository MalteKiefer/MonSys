import { keepPreviousData, useQuery } from "@tanstack/react-query";
import { Search } from "lucide-react";
import { useEffect, useMemo, useState } from "react";
import { Link } from "react-router-dom";

import {
  Empty,
  Panel,
  PanelBody,
  PanelHeader,
  SectionHeading,
  TBody,
  TD,
  TH,
  THead,
  Table,
  TextInput,
} from "../components/ui";
import { api } from "../lib/api";
import { GlobalPackageRow, Host } from "../lib/types";
import { hostDisplay } from "../lib/utils";

const MANAGERS = ["", "dpkg", "rpm", "pacman", "apk"] as const;
const PAGE_SIZE = 50;

type Resp = { total: number; limit: number; offset: number; packages: GlobalPackageRow[] };
type HostsResp = { hosts: Host[] };

export function Packages() {
  const [q, setQ] = useState("");
  const [debounced, setDebounced] = useState("");
  const [manager, setManager] = useState<string>("");
  const [hostID, setHostID] = useState<string>("");
  const [offset, setOffset] = useState(0);

  // Debounce the query so we don't hit the server on every keystroke.
  useEffect(() => {
    const t = setTimeout(() => setDebounced(q.trim()), 250);
    return () => clearTimeout(t);
  }, [q]);

  // Reset pagination when filters change.
  useEffect(() => {
    setOffset(0);
  }, [debounced, manager, hostID]);

  const hosts = useQuery({
    queryKey: ["hosts-for-filter"],
    queryFn: () => api<HostsResp>("/v1/hosts"),
  });

  const params = useMemo(() => {
    const u = new URLSearchParams();
    if (debounced) u.set("q", debounced);
    if (manager) u.set("manager", manager);
    if (hostID) u.set("host_id", hostID);
    u.set("limit", String(PAGE_SIZE));
    u.set("offset", String(offset));
    return u.toString();
  }, [debounced, manager, hostID, offset]);

  const search = useQuery({
    queryKey: ["packages", params],
    queryFn: () => api<Resp>(`/v1/packages?${params}`),
    placeholderData: keepPreviousData,
  });

  const data = search.data;
  const total = data?.total ?? 0;
  const rows = data?.packages ?? [];

  return (
    <div className="mx-auto max-w-7xl space-y-4 p-6">
      <div className="flex items-center justify-between">
        <SectionHeading>Packages</SectionHeading>
        <p className="text-xs tabular-nums text-fg-subtle">{total} matches</p>
      </div>

      <Panel>
        <PanelHeader>
          <div className="flex w-full items-center gap-3">
            <div className="relative flex-1">
              <Search className="pointer-events-none absolute left-2.5 top-1/2 h-4 w-4 -translate-y-1/2 text-fg-subtle" />
              <TextInput
                type="search"
                placeholder="Search by name or version… (case-insensitive)"
                value={q}
                onChange={(e) => setQ(e.target.value)}
                className="pl-8 font-mono"
                autoFocus
              />
            </div>
            <select
              value={manager}
              onChange={(e) => setManager(e.target.value)}
              className="rounded-md border border-border bg-panel px-3 py-2 text-sm text-fg focus:border-accent focus:outline-none"
            >
              {MANAGERS.map((m) => (
                <option key={m} value={m}>
                  {m === "" ? "All managers" : m}
                </option>
              ))}
            </select>
            <select
              value={hostID}
              onChange={(e) => setHostID(e.target.value)}
              className="max-w-[220px] truncate rounded-md border border-border bg-panel px-3 py-2 text-sm text-fg focus:border-accent focus:outline-none"
            >
              <option value="">All hosts</option>
              {hosts.data?.hosts?.map((h) => (
                <option key={h.id} value={h.id}>
                  {hostDisplay(h)}
                </option>
              ))}
            </select>
          </div>
        </PanelHeader>
        <PanelBody className="p-0">
          {search.isLoading ? (
            <p className="px-5 py-8 text-sm text-fg-subtle">Loading…</p>
          ) : rows.length === 0 ? (
            <Empty>No packages match.</Empty>
          ) : (
            <Table>
              <THead>
                <tr>
                  <TH>Manager</TH>
                  <TH>Name</TH>
                  <TH>Version</TH>
                  <TH>Arch</TH>
                  <TH>Source</TH>
                  <TH>Host</TH>
                </tr>
              </THead>
              <TBody>
                {rows.map((p, i) => (
                  <tr key={`${p.host_id}-${p.manager}-${p.name}-${i}`} className="hover:bg-panel-2">
                    <TD className="text-fg-muted">{p.manager}</TD>
                    <TD className="font-mono text-fg">{p.name}</TD>
                    <TD className="font-mono text-xs text-fg-muted">{p.version}</TD>
                    <TD className="text-fg-subtle">{p.arch || "—"}</TD>
                    <TD className="text-fg-subtle">{p.source_repo || "—"}</TD>
                    <TD>
                      <Link
                        to={`/hosts/${p.host_id}`}
                        className="text-accent hover:text-accent-hover hover:underline"
                      >
                        {/* TODO: pass labels for display */}
                        {p.hostname}
                      </Link>
                    </TD>
                  </tr>
                ))}
              </TBody>
            </Table>
          )}
        </PanelBody>
        {total > PAGE_SIZE && (
          <div className="flex items-center justify-between border-t border-border px-5 py-3 text-xs text-fg-muted">
            <span className="tabular-nums">
              {offset + 1}–{Math.min(offset + rows.length, total)} of {total}
            </span>
            <div className="flex items-center gap-2">
              <button
                disabled={offset === 0}
                onClick={() => setOffset(Math.max(0, offset - PAGE_SIZE))}
                className="rounded-md border border-border px-2 py-1 hover:bg-panel-2 disabled:opacity-40"
              >
                Prev
              </button>
              <button
                disabled={offset + PAGE_SIZE >= total}
                onClick={() => setOffset(offset + PAGE_SIZE)}
                className="rounded-md border border-border px-2 py-1 hover:bg-panel-2 disabled:opacity-40"
              >
                Next
              </button>
            </div>
          </div>
        )}
      </Panel>
    </div>
  );
}
