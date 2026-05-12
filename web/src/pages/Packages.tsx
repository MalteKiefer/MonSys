import { keepPreviousData, useQuery } from "@tanstack/react-query";
import { ChevronDown, ChevronRight, Search, ShieldAlert } from "lucide-react";
import { useEffect, useMemo, useState } from "react";
import { Link } from "react-router-dom";

import { Page } from "../components/page";
import {
  Empty,
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
import { useT } from "../i18n/useT";
import { api } from "../lib/api";
import type { GlobalPackageRow, Host } from "../lib/types";
import { hostDisplay } from "../lib/utils";

// Manager option list. Internally the API uses dpkg/rpm/pacman/apk; the spec
// asks for human-friendly labels (apt/dnf/pacman/apk) — we map between them.
const MANAGER_KEYS: { value: string; labelKey: string }[] = [
  { value: "", labelKey: "packages:managers.all" },
  { value: "dpkg", labelKey: "packages:managers.dpkg" },
  { value: "rpm", labelKey: "packages:managers.rpm" },
  { value: "pacman", labelKey: "packages:managers.pacman" },
  { value: "apk", labelKey: "packages:managers.apk" },
];

const PAGE_SIZE = 200;
const HOST_PREVIEW = 5;

interface Resp { total: number; limit: number; offset: number; packages: GlobalPackageRow[] }
interface HostsResp { hosts: Host[] }

// `is_security` isn't on GlobalPackageRow. As a best-effort heuristic for the
// security-only toggle, look for `*-security` source repos (Debian/Ubuntu's
// suite naming) or "security" tokens in the source path. Other distros tend
// not to encode that signal here, so the toggle is a pragmatic filter, not a
// CVE classifier.
function isSecurityRow(p: GlobalPackageRow): boolean {
  const repo = (p.source_repo ?? "").toLowerCase();
  return repo.includes("security");
}

export function Packages() {
  const { t } = useT(["packages", "common"]);
  const [q, setQ] = useState("");
  const [debounced, setDebounced] = useState("");
  const [manager, setManager] = useState<string>("");
  const [hostID, setHostID] = useState<string>("");
  const [securityOnly, setSecurityOnly] = useState(false);
  const [offset, setOffset] = useState(0);
  // Track which host sections are expanded. Default = collapsed; we open the
  // first group below so the user always sees something useful at-a-glance.
  const [expanded, setExpanded] = useState<Record<string, boolean>>({});

  // Debounce the query so we don't hit the server on every keystroke.
  useEffect(() => {
    const t = setTimeout(() => { setDebounced(q.trim()); }, 250);
    return () => { clearTimeout(t); };
  }, [q]);

  // Reset pagination when filters change.
  useEffect(() => {
    setOffset(0);
  }, [debounced, manager, hostID, securityOnly]);

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
  const allRows = data?.packages ?? [];
  const rows = useMemo(
    () => (securityOnly ? allRows.filter(isSecurityRow) : allRows),
    [allRows, securityOnly],
  );

  // Group rows by host for the collapsible-per-host layout. Order groups by
  // descending row count so heavy hosts sort first.
  const groups = useMemo(() => {
    const byHost = new Map<string, { hostID: string; hostname: string; rows: GlobalPackageRow[] }>();
    for (const r of rows) {
      const cur = byHost.get(r.host_id) ?? { hostID: r.host_id, hostname: r.hostname, rows: [] };
      cur.rows.push(r);
      byHost.set(r.host_id, cur);
    }
    return [...byHost.values()].sort((a, b) => b.rows.length - a.rows.length);
  }, [rows]);

  // Auto-expand the first group when results change so the page never reads
  // as a wall of collapsed sections.
  useEffect(() => {
    if (groups.length === 0) return;
    setExpanded((prev) => {
      if (Object.keys(prev).length > 0) return prev;
      return { [groups[0].hostID]: true };
    });
  }, [groups]);

  // Map host_id → Host so we can render the canonical hostDisplay (which
  // also surfaces friendly labels). The packages endpoint only returns the
  // raw hostname; this lookup fills in the rest.
  const hostByID = useMemo(() => {
    const m = new Map<string, Host>();
    for (const h of hosts.data?.hosts ?? []) m.set(h.id, h);
    return m;
  }, [hosts.data?.hosts]);

  function toggle(id: string) {
    setExpanded((prev) => ({ ...prev, [id]: !prev[id] }));
  }

  const subtitle = (
    <span className="tabular-nums">
      {securityOnly ? t("packages:subtitleSecurity", { count: rows.length }) : ""}
      {t("packages:subtitleTotal", { count: total })}
    </span>
  );

  return (
    <Page title={t("packages:title")} subtitle={subtitle}>
      <Panel>
        <PanelHeader>
          <div className="flex w-full flex-wrap items-center gap-3">
            <div className="relative min-w-[220px] flex-1">
              <Search className="pointer-events-none absolute left-2.5 top-1/2 h-4 w-4 -translate-y-1/2 text-fg-subtle" />
              <TextInput
                type="search"
                placeholder={t("packages:filters.searchPlaceholder")}
                value={q}
                onChange={(e) => { setQ(e.target.value); }}
                className="pl-8 font-mono"
                autoFocus
              />
            </div>
            <select
              value={manager}
              onChange={(e) => { setManager(e.target.value); }}
              className="rounded-md border border-border bg-panel px-3 py-2 text-sm text-fg focus:border-accent focus:outline-none"
              aria-label={t("packages:filters.managerAria")}
            >
              {MANAGER_KEYS.map((m) => (
                <option key={m.value} value={m.value}>
                  {t(m.labelKey)}
                </option>
              ))}
            </select>
            <select
              value={hostID}
              onChange={(e) => { setHostID(e.target.value); }}
              className="max-w-[220px] truncate rounded-md border border-border bg-panel px-3 py-2 text-sm text-fg focus:border-accent focus:outline-none"
              aria-label={t("packages:filters.hostAria")}
            >
              <option value="">{t("packages:filters.allHosts")}</option>
              {hosts.data?.hosts?.map((h) => (
                <option key={h.id} value={h.id}>
                  {hostDisplay(h)}
                </option>
              ))}
            </select>
            <label className="inline-flex cursor-pointer items-center gap-2 rounded-md border border-border bg-panel px-3 py-2 text-sm text-fg-muted hover:border-border-strong">
              <input
                type="checkbox"
                checked={securityOnly}
                onChange={(e) => { setSecurityOnly(e.target.checked); }}
                className="h-3.5 w-3.5 accent-accent"
              />
              <ShieldAlert className={`h-3.5 w-3.5 ${securityOnly ? "text-warn" : "text-fg-subtle"}`} />
              {t("packages:filters.securityOnly")}
            </label>
          </div>
        </PanelHeader>
        <PanelBody className="p-0">
          {search.isLoading ? (
            <div className="p-5">
              <Skeleton className="h-48" />
            </div>
          ) : groups.length === 0 ? (
            <Empty>{t("packages:empty")}</Empty>
          ) : (
            <ul className="divide-y divide-border">
              {groups.map((g) => {
                const isOpen = !!expanded[g.hostID];
                const host = hostByID.get(g.hostID);
                const display = host ? hostDisplay(host) : g.hostname;
                const preview = isOpen ? g.rows : g.rows.slice(0, HOST_PREVIEW);
                const remaining = Math.max(0, g.rows.length - HOST_PREVIEW);
                return (
                  <li key={g.hostID}>
                    <button
                      type="button"
                      onClick={() => { toggle(g.hostID); }}
                      aria-expanded={isOpen}
                      className="flex w-full items-center gap-2 px-5 py-2.5 text-left text-sm font-medium text-fg hover:bg-panel-2"
                    >
                      {isOpen ? (
                        <ChevronDown className="h-3.5 w-3.5 text-fg-subtle" />
                      ) : (
                        <ChevronRight className="h-3.5 w-3.5 text-fg-subtle" />
                      )}
                      <Link
                        to={`/hosts/${g.hostID}`}
                        onClick={(e) => { e.stopPropagation(); }}
                        className="hover:text-accent hover:underline"
                      >
                        {display}
                      </Link>
                      <span className="ml-auto text-xs tabular-nums text-fg-subtle">
                        {g.rows.length} {g.rows.length === 1 ? t("packages:group.packageOne") : t("packages:group.packageOther")}
                      </span>
                    </button>
                    {(isOpen || preview.length > 0) && (
                      <div className="overflow-x-auto">
                        <Table>
                          <THead>
                            <tr>
                              <TH>{t("packages:table.manager")}</TH>
                              <TH>{t("packages:table.name")}</TH>
                              <TH>{t("packages:table.version")}</TH>
                              <TH>{t("packages:table.arch")}</TH>
                              <TH>{t("packages:table.source")}</TH>
                            </tr>
                          </THead>
                          <TBody>
                            {preview.map((p, i) => (
                              <tr
                                key={`${p.host_id}-${p.manager}-${p.name}-${i}`}
                                className="hover:bg-panel-2"
                              >
                                <TD className="text-fg-muted">{p.manager}</TD>
                                <TD className="font-mono text-fg">{p.name}</TD>
                                <TD className="font-mono text-xs text-fg-muted">{p.version}</TD>
                                <TD className="text-fg-subtle">{p.arch || "—"}</TD>
                                <TD
                                  className={
                                    isSecurityRow(p) ? "text-warn" : "text-fg-subtle"
                                  }
                                >
                                  {p.source_repo || "—"}
                                </TD>
                              </tr>
                            ))}
                          </TBody>
                        </Table>
                      </div>
                    )}
                    {!isOpen && remaining > 0 && (
                      <button
                        type="button"
                        onClick={() => { toggle(g.hostID); }}
                        className="block w-full px-5 py-2 text-left text-xs text-accent hover:bg-panel-2 hover:underline"
                      >
                        {t("packages:group.viewAll", { count: g.rows.length })}
                      </button>
                    )}
                  </li>
                );
              })}
            </ul>
          )}
        </PanelBody>
        {total > PAGE_SIZE && (
          <div className="flex items-center justify-between border-t border-border px-5 py-3 text-xs text-fg-muted">
            <span className="tabular-nums">
              {t("packages:pagination.range", { from: offset + 1, to: Math.min(offset + allRows.length, total), total })}
            </span>
            <div className="flex items-center gap-2">
              <button
                disabled={offset === 0}
                onClick={() => { setOffset(Math.max(0, offset - PAGE_SIZE)); }}
                className="rounded-md border border-border px-2 py-1 hover:bg-panel-2 disabled:opacity-40"
              >
                {t("packages:pagination.prev")}
              </button>
              <button
                disabled={offset + PAGE_SIZE >= total}
                onClick={() => { setOffset(offset + PAGE_SIZE); }}
                className="rounded-md border border-border px-2 py-1 hover:bg-panel-2 disabled:opacity-40"
              >
                {t("packages:pagination.next")}
              </button>
            </div>
          </div>
        )}
      </Panel>
    </Page>
  );
}
