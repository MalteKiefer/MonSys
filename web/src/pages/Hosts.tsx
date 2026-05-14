import { useQuery } from "@tanstack/react-query";
import { AlertTriangle, Plus, Server } from "lucide-react";
import type { KeyboardEvent} from "react";
import { useMemo, useState } from "react";
import { useNavigate } from "react-router-dom";

import { EnrollAgentModal } from "../components/EnrollAgentModal";
import { DistroIcon } from "../components/icons/DistroIcon";
import { ServiceBadges } from "../components/icons/ServiceIcon";
import { EmptyState, ErrorState, Page } from "../components/page";
import {
  Button,
  Field,
  Panel,
  StatusPill,
  TBody,
  TD,
  TH,
  THead,
  Table,
  TextInput,
} from "../components/ui";
import { useT } from "../i18n/useT";
import { api } from "../lib/api";
import { useAuth } from "../lib/auth";
import type { Host } from "../lib/types";
import { hostDisplay } from "../lib/utils";

interface HostsResponse { hosts: Host[] }

type StatusFilter = "all" | "online" | "stale" | "offline";

const STATUS_FILTER_KEYS: { key: StatusFilter; labelKey: string }[] = [
  { key: "all", labelKey: "hosts:filters.all" },
  { key: "online", labelKey: "hosts:filters.online" },
  { key: "stale", labelKey: "hosts:filters.stale" },
  { key: "offline", labelKey: "hosts:filters.offline" },
];

export function Hosts() {
  const { t } = useT(["hosts", "common"]);
  const navigate = useNavigate();
  const isAdmin = useAuth((s) => s.user?.role === "admin");
  const { data, isLoading, error, refetch } = useQuery({
    queryKey: ["hosts"],
    queryFn: () => api<HostsResponse>("/v1/hosts"),
    refetchInterval: 15_000,
  });

  const [search, setSearch] = useState("");
  const [statusFilter, setStatusFilter] = useState<StatusFilter>("all");
  const [enrollOpen, setEnrollOpen] = useState(false);
  // Sort modes for the Updates column. Cycles: off → pending desc → security desc → off.
  const [updatesSort, setUpdatesSort] = useState<"off" | "pending" | "security">("off");

  const hosts = useMemo(() => data?.hosts ?? [], [data]);

  const filtered = useMemo(() => {
    const needle = search.trim().toLowerCase();
    const out = hosts.filter((h) => {
      if (statusFilter !== "all" && h.status !== statusFilter) return false;
      if (needle === "") return true;
      if (hostDisplay(h).toLowerCase().includes(needle)) return true;
      if ((h.tags ?? []).some((t) => t.toLowerCase().includes(needle))) return true;
      return false;
    });
    if (updatesSort === "off") return out;
    const key = updatesSort === "security" ? "security_updates" : "pending_updates";
    // Stable desc; hosts without package data sink to the bottom.
    return [...out].sort((a, b) => {
      const av = a[key];
      const bv = b[key];
      const aDef = typeof av === "number";
      const bDef = typeof bv === "number";
      if (!aDef && !bDef) return 0;
      if (!aDef) return 1;
      if (!bDef) return -1;
      return (bv) - (av);
    });
  }, [hosts, search, statusFilter, updatesSort]);

  const cycleUpdatesSort = () => {
    setUpdatesSort((s) => (s === "off" ? "pending" : s === "pending" ? "security" : "off"));
  };

  const updatesSortLabel =
    updatesSort === "pending"
      ? t("hosts:table.sortPendingDesc")
      : updatesSort === "security"
        ? t("hosts:table.sortSecurityDesc")
        : t("hosts:table.sortUnsorted");
  const updatesSortGlyph = updatesSort === "pending" ? "↓" : updatesSort === "security" ? "⚠↓" : "";

  const onRowKeyDown = (e: KeyboardEvent<HTMLTableRowElement>, id: string) => {
    if (e.key === "Enter" || e.key === " ") {
      e.preventDefault();
      void navigate(`/hosts/${id}`);
    }
  };

  const addAgentButton = isAdmin ? (
    <Button variant="primary" onClick={() => { setEnrollOpen(true); }}>
      <Plus className="h-3.5 w-3.5" /> {t("hosts:addAgent")}
    </Button>
  ) : null;

  const subtitle = !isLoading && !error ? (
    <span className="tabular-nums">
      {filtered.length === hosts.length
        ? t("hosts:subtitleKnown", { count: hosts.length })
        : t("hosts:subtitleFiltered", { shown: filtered.length, total: hosts.length })}
    </span>
  ) : undefined;

  return (
    <Page title={t("hosts:title")} subtitle={subtitle} actions={addAgentButton}>
      {enrollOpen && <EnrollAgentModal onClose={() => { setEnrollOpen(false); }} />}

      {isLoading ? (
        <p className="text-sm text-fg-muted">{t("hosts:loading")}</p>
      ) : error ? (
        <ErrorState message={(error).message} onRetry={() => { void refetch(); }} />
      ) : (
        <>
          {hosts.length > 0 && (
            <div className="flex flex-col gap-3 sm:flex-row sm:items-end sm:gap-4">
              <div className="flex-1">
                <Field label={t("hosts:filters.searchLabel")} hint={t("hosts:filters.searchHint")}>
                  <TextInput
                    type="search"
                    placeholder={t("hosts:filters.searchPlaceholder")}
                    value={search}
                    onChange={(e) => { setSearch(e.currentTarget.value); }}
                    aria-label={t("hosts:filters.searchAria")}
                  />
                </Field>
              </div>
              <div>
                <Field label={t("hosts:filters.statusLabel")}>
                  <div
                    role="group"
                    aria-label={t("hosts:filters.statusGroupAria")}
                    className="inline-flex rounded-md border border-border bg-panel p-0.5"
                  >
                    {STATUS_FILTER_KEYS.map(({ key, labelKey }) => {
                      const active = key === statusFilter;
                      return (
                        <button
                          key={key}
                          type="button"
                          aria-pressed={active}
                          onClick={() => { setStatusFilter(key); }}
                          className={`rounded px-2.5 py-1 text-xs font-medium transition-colors duration-150 ${
                            active ? "bg-panel-2 text-fg shadow-panel" : "text-fg-subtle hover:text-fg"
                          }`}
                        >
                          {t(labelKey)}
                        </button>
                      );
                    })}
                  </div>
                </Field>
              </div>
            </div>
          )}

          <Panel>
            {hosts.length === 0 ? (
              <EmptyState
                icon={Server}
                title={t("hosts:empty.noneTitle")}
                hint={
                  <>
                    {t("hosts:empty.noneHintPrefix")}<code className="font-mono text-fg-muted">{t("hosts:empty.noneHintCode")}</code>{t("hosts:empty.noneHintSuffix")}
                  </>
                }
                primaryAction={addAgentButton ?? undefined}
              />
            ) : filtered.length === 0 ? (
              <EmptyState icon={Server} title={t("hosts:empty.noMatch")} />
            ) : (
              // Wide host table — let the scroll live inside the panel on
              // mobile so the body itself never gets a horizontal scrollbar.
              // On md+ the table fits, so drop the overflow-x container
              // entirely: CSS implicitly promotes the perpendicular axis to
              // `auto` whenever one axis is non-visible, which would otherwise
              // surface a spurious vertical scrollbar on desktop.
              <div className="overflow-x-auto md:overflow-x-visible">
              <Table>
                <THead>
                  <tr>
                    <TH>{t("hosts:table.status")}</TH>
                    <TH>{t("hosts:table.host")}</TH>
                    <TH>{t("hosts:table.distro")}</TH>
                    <TH>{t("hosts:table.services")}</TH>
                    <TH>{t("hosts:table.tagsGroups")}</TH>
                    <TH>{t("hosts:table.cpuRam")}</TH>
                    <TH>{t("hosts:table.agent")}</TH>
                    <TH>
                      <button
                        type="button"
                        onClick={cycleUpdatesSort}
                        aria-label={t("hosts:table.updatesColAria", { label: updatesSortLabel })}
                        className="inline-flex items-center gap-1 text-inherit hover:text-fg focus:outline-none focus-visible:underline"
                      >
                        {t("hosts:table.updates")}
                        {updatesSortGlyph && (
                          <span aria-hidden="true" className="text-[10px] text-accent">
                            {updatesSortGlyph}
                          </span>
                        )}
                      </button>
                    </TH>
                    <TH>{t("hosts:table.lastSeen")}</TH>
                  </tr>
                </THead>
                <TBody>
                  {filtered.map((h) => (
                    <tr
                      key={h.id}
                      role="link"
                      tabIndex={0}
                      aria-label={t("hosts:table.openHostAria", { host: hostDisplay(h) })}
                      className="cursor-pointer transition-colors duration-100 hover:bg-panel-2 focus:outline-none focus-visible:bg-panel-2 focus-visible:ring-2 focus-visible:ring-inset focus-visible:ring-accent/50"
                      onClick={() => { void navigate(`/hosts/${h.id}`); }}
                      onKeyDown={(e) => { onRowKeyDown(e, h.id); }}
                    >
                      <TD><StatusPill status={h.status} /></TD>
                      <TD>
                        <span className="font-medium text-fg">{hostDisplay(h)}</span>
                        <span className="ml-2 font-mono text-[10px] text-fg-subtle">{h.arch}</span>
                      </TD>
                      <TD>
                        <span className="inline-flex items-center gap-1.5">
                          <DistroIcon family={h.distro_family} />
                          <span className="text-fg-muted">{h.distro || "—"}</span>
                        </span>
                      </TD>
                      <TD>
                        <ServiceBadges services={h.services} />
                        {(!h.services || h.services.length === 0) && (
                          <span className="text-fg-subtle">—</span>
                        )}
                      </TD>
                      <TD>
                        <div className="flex flex-wrap items-center gap-1">
                          {(h.tags ?? []).map((t) => (
                            <span key={t} className="rounded-md bg-panel-2 px-1.5 py-0.5 font-mono text-[10px] text-accent">
                              #{t}
                            </span>
                          ))}
                          {(h.groups ?? []).map((g) => (
                            <span
                              key={g.id}
                              className="rounded-md bg-info/10 px-1.5 py-0.5 font-mono text-[10px] text-info ring-1 ring-inset ring-info/30"
                            >
                              {g.name}
                            </span>
                          ))}
                          {(h.tags ?? []).length === 0 && (h.groups ?? []).length === 0 && (
                            <span className="text-fg-subtle">—</span>
                          )}
                        </div>
                      </TD>
                      <TD className="tabular-nums text-fg-muted whitespace-nowrap">
                        {t("hosts:table.cpuRamValue", { cores: h.cpu_cores, ram: formatBytes(h.ram_total_bytes) })}
                      </TD>
                      <TD className="font-mono text-xs text-fg-muted whitespace-nowrap">
                        {h.agent_version || "—"}
                      </TD>
                      <TD className="tabular-nums whitespace-nowrap">
                        <UpdatesCell pending={h.pending_updates} security={h.security_updates} t={t} />
                      </TD>
                      <TD className="text-fg-muted whitespace-nowrap">{relativeTime(h.last_seen_at, t)}</TD>
                    </tr>
                  ))}
                </TBody>
              </Table>
              </div>
            )}
          </Panel>
        </>
      )}
    </Page>
  );
}

function UpdatesCell({
  pending,
  security,
  t,
}: {
  pending?: number;
  security?: number;
  t: (key: string, opts?: Record<string, unknown>) => string;
}) {
  if (typeof pending !== "number") {
    return <span className="text-fg-subtle">—</span>;
  }
  const sec = typeof security === "number" ? security : 0;
  if (sec > 0) {
    return (
      <span
        className="inline-flex items-center gap-1 text-warn"
        title={t("hosts:table.securityTitle", { count: sec })}
      >
        <AlertTriangle className="h-3 w-3" aria-hidden="true" />
        {pending}
      </span>
    );
  }
  return <span className="text-fg-muted">{pending}</span>;
}

function formatBytes(n: number): string {
  if (!n) return "—";
  const units = ["B", "KiB", "MiB", "GiB", "TiB"];
  let v = n;
  let i = 0;
  while (v >= 1024 && i < units.length - 1) {
    v /= 1024;
    i++;
  }
  return `${v.toFixed(v < 10 && i > 0 ? 1 : 0)} ${units[i]}`;
}

function relativeTime(iso: string, t: (key: string, opts?: Record<string, unknown>) => string): string {
  const ts = new Date(iso).getTime();
  if (Number.isNaN(ts)) return iso;
  const diff = (Date.now() - ts) / 1000;
  if (diff < 60) return t("hosts:time.secondsAgo", { count: Math.round(diff) });
  if (diff < 3600) return t("hosts:time.minutesAgo", { count: Math.round(diff / 60) });
  if (diff < 86400) return t("hosts:time.hoursAgo", { count: Math.round(diff / 3600) });
  return t("hosts:time.daysAgo", { count: Math.round(diff / 86400) });
}
