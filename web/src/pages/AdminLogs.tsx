import { keepPreviousData, useQuery } from "@tanstack/react-query";
import { Pause, Play, Search } from "lucide-react";
import { ReactNode, useEffect, useMemo, useRef, useState } from "react";
import { Link } from "react-router-dom";

import { Page } from "../components/page";
import {
  Button,
  Empty,
  Panel,
  PanelBody,
  PanelHeader,
  Skeleton,
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
import { Host, ServerLogEntry } from "../lib/types";
import { hostDisplay } from "../lib/utils";

const PAGE_SIZE = 100;

// Server-side level dropdown (single value). Kept alongside the new chip
// filters so deep filtering by a specific log level still hits the API.
const LEVELS = ["", "debug", "info", "warn", "error"] as const;

// Client-side multi-select chips. Levels are matched against the entry's
// `level` field (uppercase). Op chips look at `attrs.op`; we infer the
// dominant verbs from the codebase rather than guessing — clicking the
// star chip means "all ops", which is a quick way to clear the op
// selection.
const LEVEL_CHIPS: ReadonlyArray<{ key: ServerLogEntry["level"]; label: string }> = [
  { key: "INFO", label: "INFO" },
  { key: "WARN", label: "WARN" },
  { key: "ERROR", label: "ERROR" },
];

const OP_CHIPS = ["auth", "register", "ingest", "rule", "channel", "agent", "*"] as const;
type OpChip = (typeof OP_CHIPS)[number];

type Resp = {
  total: number;
  limit: number;
  offset: number;
  entries: ServerLogEntry[];
  seq: number;
};

// AdminLogsContent renders the toolbar + table body without the outer
// `<Page>` wrapper. The consolidated /admin/logs view mounts it inside a
// tab panel and forwards the header counter via `onMeta`.
export function AdminLogsContent({ onMeta }: { onMeta?: (node: ReactNode) => void } = {}) {
  const { t } = useT(["admin", "common"]);
  const [q, setQ] = useState("");
  const [debounced, setDebounced] = useState("");
  const [level, setLevel] = useState<(typeof LEVELS)[number]>("");
  const [hostID, setHostID] = useState<string>("");
  const [offset, setOffset] = useState(0);
  // Default off so refetches don't hammer the server unsolicited; users
  // opt in via the Live tail toggle.
  const [liveTail, setLiveTail] = useState(false);

  // Multi-select chip filters (client-side over the page we just fetched).
  const [chipLevels, setChipLevels] = useState<Set<ServerLogEntry["level"]>>(new Set());
  const [chipOps, setChipOps] = useState<Set<OpChip>>(new Set());

  useEffect(() => {
    const t = setTimeout(() => setDebounced(q.trim()), 250);
    return () => clearTimeout(t);
  }, [q]);
  useEffect(() => setOffset(0), [debounced, level, hostID]);

  const hosts = useQuery({
    queryKey: ["hosts-for-filter"],
    queryFn: () => api<{ hosts: Host[] }>("/v1/hosts"),
  });
  const hostMap = useMemo(() => {
    const m = new Map<string, string>();
    for (const h of hosts.data?.hosts ?? []) m.set(h.id, hostDisplay(h));
    return m;
  }, [hosts.data]);

  const params = useMemo(() => {
    const u = new URLSearchParams();
    if (debounced) u.set("q", debounced);
    if (level) u.set("level", level);
    if (hostID) u.set("host_id", hostID);
    u.set("limit", String(PAGE_SIZE));
    u.set("offset", String(offset));
    return u.toString();
  }, [debounced, level, hostID, offset]);

  const logs = useQuery({
    queryKey: ["admin-logs", params],
    queryFn: () => api<Resp>(`/v1/admin/logs?${params}`),
    placeholderData: keepPreviousData,
    refetchInterval: liveTail ? 2_000 : false,
  });

  const rawEntries = logs.data?.entries ?? [];
  const total = logs.data?.total ?? 0;

  // Apply chip filters client-side. Empty set = no filter for that
  // dimension (the user doesn't expect "no chips" to mean "no rows").
  const entries = useMemo(() => {
    const wantLevels = chipLevels;
    const wantOps = chipOps;
    const wildcard = wantOps.has("*");
    return rawEntries.filter((e) => {
      if (wantLevels.size > 0 && !wantLevels.has(e.level)) return false;
      if (wantOps.size > 0 && !wildcard) {
        const op = typeof e.attrs?.op === "string" ? (e.attrs.op as string) : "";
        // Match by prefix so e.g. "auth" picks up "auth.login", "auth.logout".
        let hit = false;
        for (const want of wantOps) {
          if (want === "*") continue;
          if (op === want || op.startsWith(`${want}.`) || op.startsWith(`${want}_`)) {
            hit = true;
            break;
          }
        }
        if (!hit) return false;
      }
      return true;
    });
  }, [rawEntries, chipLevels, chipOps]);

  // Auto-scroll to top when new entries arrive in live mode. We watch
  // the freshest seq number and the live-tail toggle so a manual refetch
  // also scrolls if the user explicitly enabled tailing.
  const tableTopRef = useRef<HTMLDivElement | null>(null);
  const lastSeqRef = useRef<number>(logs.data?.seq ?? 0);
  useEffect(() => {
    const seq = logs.data?.seq ?? 0;
    if (liveTail && seq > lastSeqRef.current) {
      tableTopRef.current?.scrollIntoView({ behavior: "smooth", block: "start" });
    }
    lastSeqRef.current = seq;
  }, [logs.data?.seq, liveTail]);

  function toggleChipLevel(k: ServerLogEntry["level"]) {
    setChipLevels((prev) => {
      const next = new Set(prev);
      if (next.has(k)) next.delete(k);
      else next.add(k);
      return next;
    });
  }
  function toggleChipOp(k: OpChip) {
    setChipOps((prev) => {
      const next = new Set(prev);
      if (next.has(k)) next.delete(k);
      else next.add(k);
      return next;
    });
  }

  // Surface header meta (count + seq) to the parent. We re-publish whenever
  // the underlying numbers change so the tab container can host it in the
  // page header beside the tab strip.
  const seq = logs.data?.seq ?? 0;
  useEffect(() => {
    if (!onMeta) return;
    onMeta(
      <span className="text-xs text-fg-subtle tabular-nums">
        {t("admin:logs.meta", { total, seq })}
      </span>,
    );
    return () => onMeta(null);
  }, [onMeta, total, seq, t]);

  return (
    <>
      <Panel>
        <PanelHeader>
          <div className="flex w-full flex-wrap items-center gap-2">
            <div className="relative flex-1 min-w-[240px]">
              <Search className="pointer-events-none absolute left-2.5 top-1/2 h-4 w-4 -translate-y-1/2 text-fg-subtle" />
              <TextInput
                type="search"
                placeholder={t("admin:logs.search_placeholder")}
                value={q}
                onChange={(e) => setQ(e.target.value)}
                className="pl-8 font-mono"
                autoFocus
              />
            </div>
            <select
              value={level}
              onChange={(e) => setLevel(e.target.value as (typeof LEVELS)[number])}
              className="rounded-md border border-border bg-panel px-3 py-2 text-sm focus:border-accent focus:outline-none"
            >
              {LEVELS.map((l) => (
                <option key={l} value={l}>{l === "" ? t("admin:logs.any_level") : l}</option>
              ))}
            </select>
            <select
              value={hostID}
              onChange={(e) => setHostID(e.target.value)}
              className="max-w-[220px] truncate rounded-md border border-border bg-panel px-3 py-2 text-sm focus:border-accent focus:outline-none"
            >
              <option value="">{t("admin:logs.all_hosts")}</option>
              {(hosts.data?.hosts ?? []).map((h) => (
                <option key={h.id} value={h.id}>
                  {hostDisplay(h)}
                </option>
              ))}
            </select>
            <Button
              variant={liveTail ? "primary" : "secondary"}
              size="sm"
              onClick={() => setLiveTail((v) => !v)}
              aria-pressed={liveTail}
              title={liveTail ? t("admin:logs.pause_tail_title") : t("admin:logs.start_tail_title")}
            >
              {liveTail ? (
                <>
                  <Pause className="h-3.5 w-3.5" /> {t("admin:logs.live_tail")}
                </>
              ) : (
                <>
                  <Play className="h-3.5 w-3.5" /> {t("admin:logs.live_tail")}
                </>
              )}
            </Button>
          </div>
        </PanelHeader>

        <div className="flex flex-wrap items-center gap-1.5 border-b border-border px-5 py-2.5">
          <span className="mr-1 text-[10px] font-semibold uppercase tracking-wider text-fg-subtle">
            {t("admin:logs.chip_level")}
          </span>
          {LEVEL_CHIPS.map((c) => (
            <Chip
              key={c.key}
              active={chipLevels.has(c.key)}
              onClick={() => toggleChipLevel(c.key)}
            >
              {c.label}
            </Chip>
          ))}
          <span className="mx-2 h-4 w-px bg-border" aria-hidden />
          <span className="mr-1 text-[10px] font-semibold uppercase tracking-wider text-fg-subtle">
            {t("admin:logs.chip_op")}
          </span>
          {OP_CHIPS.map((op) => (
            <Chip key={op} active={chipOps.has(op)} onClick={() => toggleChipOp(op)}>
              {op}
            </Chip>
          ))}
          {(chipLevels.size > 0 || chipOps.size > 0) && (
            <button
              type="button"
              onClick={() => {
                setChipLevels(new Set());
                setChipOps(new Set());
              }}
              className="ml-auto text-[11px] text-fg-subtle hover:text-fg"
            >
              {t("admin:logs.chip_clear")}
            </button>
          )}
        </div>

        <PanelBody className="p-0 overflow-x-auto">
          <div ref={tableTopRef} />
          {logs.isLoading ? (
            <div className="p-5">
              <Skeleton className="h-64" />
            </div>
          ) : entries.length === 0 ? (
            <Empty>{t("admin:logs.empty")}</Empty>
          ) : (
            <Table>
              <THead>
                <tr>
                  <TH>{t("admin:logs.col_time")}</TH>
                  <TH>{t("admin:logs.col_level")}</TH>
                  <TH>{t("admin:logs.col_msg")}</TH>
                  <TH>{t("admin:logs.col_attrs")}</TH>
                </tr>
              </THead>
              <TBody>
                {entries.map((e, i) => (
                  <tr key={`${e.time}-${i}`} className="hover:bg-panel-2 align-top">
                    <TD className="font-mono text-[11px] text-fg-muted whitespace-nowrap">
                      {new Date(e.time).toLocaleTimeString()}
                      <span className="ml-2 text-fg-subtle">
                        {new Date(e.time).toLocaleDateString()}
                      </span>
                    </TD>
                    <TD>
                      <StatusPill status={statusFor(e.level)}>{e.level}</StatusPill>
                    </TD>
                    <TD className="font-mono text-xs">{e.msg}</TD>
                    <TD className="font-mono text-[11px]">
                      <AttrsDisplay attrs={e.attrs} hostMap={hostMap} />
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
              {t("admin:logs.pagination_range", {
                from: offset + 1,
                to: Math.min(offset + entries.length, total),
                total,
              })}
            </span>
            <div className="flex items-center gap-2">
              <button
                disabled={offset === 0}
                onClick={() => setOffset(Math.max(0, offset - PAGE_SIZE))}
                className="rounded-md border border-border px-2 py-1 hover:bg-panel-2 disabled:opacity-40"
              >
                {t("admin:logs.prev")}
              </button>
              <button
                disabled={offset + PAGE_SIZE >= total}
                onClick={() => setOffset(offset + PAGE_SIZE)}
                className="rounded-md border border-border px-2 py-1 hover:bg-panel-2 disabled:opacity-40"
              >
                {t("admin:logs.next")}
              </button>
            </div>
          </div>
        )}
      </Panel>
    </>
  );
}

// Standalone page wrapper, retained for backwards-compat / direct imports.
// The consolidated /admin/logs route uses LogsPage instead; this is still
// useful if anything embeds the page elsewhere.
export function AdminLogs() {
  const { t } = useT(["admin", "common"]);
  return (
    <Page
      title={t("admin:logs.title")}
      subtitle={t("admin:logs.subtitle")}
      breadcrumb={[{ label: t("admin:logs.breadcrumb_admin") }, { label: t("admin:logs.breadcrumb_server_logs") }]}
    >
      <AdminLogsContent />
    </Page>
  );
}

// Compact toggle-able chip. Active styling is the spec-mandated combo.
function Chip({
  active,
  onClick,
  children,
}: {
  active: boolean;
  onClick: () => void;
  children: React.ReactNode;
}) {
  const base =
    "inline-flex items-center rounded-md px-2 py-0.5 text-[11px] font-medium font-mono transition-colors duration-150";
  const off = "border border-border bg-panel text-fg-muted hover:bg-panel-2 hover:text-fg";
  const on = "bg-panel-2 text-fg ring-1 ring-accent/40";
  return (
    <button
      type="button"
      onClick={onClick}
      aria-pressed={active}
      className={`${base} ${active ? on : off}`}
    >
      {children}
    </button>
  );
}

function statusFor(level: ServerLogEntry["level"]): "ok" | "warn" | "fail" | "info" {
  switch (level) {
    case "ERROR":
      return "fail";
    case "WARN":
      return "warn";
    case "DEBUG":
      return "info";
    default:
      return "ok";
  }
}

// AttrsDisplay renders a compact key=value list. host_id is rewritten as a
// link to /hosts/{id} (with hostname text when known); other UUIDs and IPs
// stay plain. Long string values truncate with title="full value" tooltip.
function AttrsDisplay({
  attrs,
  hostMap,
}: {
  attrs?: Record<string, unknown>;
  hostMap: Map<string, string>;
}) {
  if (!attrs || Object.keys(attrs).length === 0) {
    return <span className="text-fg-subtle">—</span>;
  }
  return (
    <div className="flex flex-wrap items-baseline gap-x-2 gap-y-0.5">
      {Object.entries(attrs).map(([k, v]) => (
        <span key={k}>
          <span className="text-fg-subtle">{k}=</span>
          {renderValue(k, v, hostMap)}
        </span>
      ))}
    </div>
  );
}

function renderValue(key: string, value: unknown, hostMap: Map<string, string>) {
  const s = typeof value === "string" ? value : JSON.stringify(value);
  if (key === "host_id" && typeof value === "string") {
    const name = hostMap.get(value);
    return (
      <Link to={`/hosts/${value}`} className="text-accent hover:underline" title={value}>
        {name ?? value.substring(0, 8)}
      </Link>
    );
  }
  if (key === "hostname" && typeof value === "string") {
    return <span className="text-fg">{value}</span>;
  }
  if (s.length > 80) {
    return (
      <span title={s} className="text-fg-muted">
        {s.substring(0, 77)}…
      </span>
    );
  }
  return <span className="text-fg-muted">{s}</span>;
}
