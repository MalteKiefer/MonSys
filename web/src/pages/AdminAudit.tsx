import { keepPreviousData, useQuery } from "@tanstack/react-query";
import { ClipboardList } from "lucide-react";
import type { ReactNode} from "react";
import { useEffect, useMemo, useState } from "react";

import { EmptyState, ErrorState, Page } from "../components/page";
import {
  Field,
  Panel,
  PanelBody,
  PanelHeader,
  TBody,
  TD,
  TH,
  THead,
  Table,
  TextInput,
} from "../components/ui";
import { useT } from "../i18n/useT";
import { api } from "../lib/api";
import type { AuditEntry } from "../lib/types";

const PAGE_SIZE = 100;

interface Resp {
  entries: AuditEntry[];
  total: number;
}

// AdminAuditContent renders the filter + table panel without the outer
// `<Page>` wrapper. The consolidated /admin/logs view mounts it inside a
// tab panel and surfaces the entry count via `onMeta`.
export function AdminAuditContent({ onMeta }: { onMeta?: (node: ReactNode) => void } = {}) {
  const { t } = useT(["admin", "common"]);
  const [actor, setActor] = useState("");
  const [action, setAction] = useState("");
  const [debouncedActor, setDebouncedActor] = useState("");
  const [debouncedAction, setDebouncedAction] = useState("");
  const [offset, setOffset] = useState(0);

  // Debounce filters so typing doesn't fire a request per keystroke.
  useEffect(() => {
    const t = setTimeout(() => { setDebouncedActor(actor.trim()); }, 250);
    return () => { clearTimeout(t); };
  }, [actor]);
  useEffect(() => {
    const t = setTimeout(() => { setDebouncedAction(action.trim()); }, 250);
    return () => { clearTimeout(t); };
  }, [action]);
   
  useEffect(() => { setOffset(0); }, [debouncedActor, debouncedAction]);

  const params = useMemo(() => {
    const u = new URLSearchParams();
    if (debouncedActor) u.set("actor", debouncedActor);
    if (debouncedAction) u.set("action", debouncedAction);
    u.set("limit", String(PAGE_SIZE));
    u.set("offset", String(offset));
    return u.toString();
  }, [debouncedActor, debouncedAction, offset]);

  const audit = useQuery({
    queryKey: ["admin-audit", params],
    queryFn: () => api<Resp>(`/v1/admin/audit?${params}`),
    placeholderData: keepPreviousData,
  });

  const total = audit.data?.total ?? 0;
  const entries = audit.data?.entries ?? [];

  // Publish the entry-count badge to the parent so the consolidated page
  // header can host it beside the tab strip.
  useEffect(() => {
    if (!onMeta) return;
    onMeta(
      <span className="text-xs text-fg-subtle tabular-nums">
        {t("admin:audit.meta", { count: total })}
      </span>,
    );
    return () => { onMeta(null); };
  }, [onMeta, total, t]);

  return (
    <Panel>
        <PanelHeader>
          <div className="flex w-full flex-wrap items-end gap-3">
            <div className="min-w-[220px] flex-1">
              <Field label={t("admin:audit.filter_actor")} hint={t("admin:audit.filter_actor_hint")}>
                <TextInput
                  type="search"
                  placeholder={t("admin:audit.filter_actor_placeholder")}
                  value={actor}
                  onChange={(e) => { setActor(e.target.value); }}
                />
              </Field>
            </div>
            <div className="min-w-[220px] flex-1">
              <Field label={t("admin:audit.filter_action")} hint={t("admin:audit.filter_action_hint")}>
                <TextInput
                  type="search"
                  placeholder={t("admin:audit.filter_action_placeholder")}
                  value={action}
                  onChange={(e) => { setAction(e.target.value); }}
                />
              </Field>
            </div>
          </div>
        </PanelHeader>
        <PanelBody className="p-0 overflow-x-auto">
          {audit.isLoading ? (
            <p className="px-5 py-4 text-sm text-fg-subtle">{t("common:actions.loading")}</p>
          ) : audit.error ? (
            <div className="p-5">
              <ErrorState
                message={(audit.error).message}
                onRetry={() => { void audit.refetch(); }}
              />
            </div>
          ) : entries.length === 0 ? (
            <EmptyState
              icon={ClipboardList}
              title={t("admin:audit.empty_title")}
              hint={t("admin:audit.empty_hint")}
            />
          ) : (
            <Table aria-label={t("admin:audit.table_label")}>
              <THead>
                <tr>
                  <TH>{t("admin:audit.col_at")}</TH>
                  <TH>{t("admin:audit.col_actor")}</TH>
                  <TH>{t("admin:audit.col_action")}</TH>
                  <TH>{t("admin:audit.col_target")}</TH>
                  <TH>{t("admin:audit.col_detail")}</TH>
                </tr>
              </THead>
              <TBody>
                {entries.map((e) => (
                  <tr key={e.id} className="hover:bg-panel-2 align-top">
                    <TD className="font-mono text-[11px] text-fg-muted whitespace-nowrap">
                      {new Date(e.at).toLocaleTimeString()}
                      <span className="ml-2 text-fg-subtle">
                        {new Date(e.at).toLocaleDateString()}
                      </span>
                    </TD>
                    <TD className="font-mono text-xs">{e.actor || "—"}</TD>
                    <TD className="font-mono text-xs text-accent">{e.action}</TD>
                    <TD className="font-mono text-[11px] text-fg-muted break-all">
                      {e.target || "—"}
                    </TD>
                    <TD className="font-mono text-[11px] text-fg-muted break-all">
                      {formatDetail(e.detail)}
                    </TD>
                  </tr>
                ))}
              </TBody>
            </Table>
          )}
        </PanelBody>
        {total > PAGE_SIZE && (
          <div
            aria-live="polite"
            className="flex items-center justify-between border-t border-border px-5 py-3 text-xs text-fg-muted"
          >
            <span className="tabular-nums">
              {t("admin:audit.pagination_range", {
                from: offset + 1,
                to: Math.min(offset + entries.length, total),
                total,
              })}
            </span>
            <div className="flex items-center gap-2">
              <button
                aria-label={t("admin:audit.prev_aria")}
                disabled={offset === 0}
                onClick={() => { setOffset(Math.max(0, offset - PAGE_SIZE)); }}
                className="rounded-md border border-border px-2 py-1 hover:bg-panel-2 disabled:opacity-40"
              >
                {t("admin:audit.prev")}
              </button>
              <button
                aria-label={t("admin:audit.next_aria")}
                disabled={offset + PAGE_SIZE >= total}
                onClick={() => { setOffset(offset + PAGE_SIZE); }}
                className="rounded-md border border-border px-2 py-1 hover:bg-panel-2 disabled:opacity-40"
              >
                {t("admin:audit.next")}
              </button>
            </div>
          </div>
        )}
      </Panel>
  );
}

// Standalone page wrapper, retained for backwards-compat. The consolidated
// /admin/logs route mounts AdminAuditContent directly inside its tab.
export function AdminAudit() {
  const { t } = useT(["admin", "common"]);
  return (
    <Page
      title={t("admin:audit.title")}
      subtitle={t("admin:audit.subtitle")}
      breadcrumb={[{ label: t("admin:audit.breadcrumb_admin") }, { label: t("admin:audit.breadcrumb_audit_log") }]}
    >
      <AdminAuditContent />
    </Page>
  );
}

// formatDetail unwraps the {"text":"..."} convention the server uses for
// non-JSON detail strings, falling back to raw JSON when the payload is
// already an object/array.
function formatDetail(raw: string): string {
  if (!raw) return "—";
  try {
    const parsed = JSON.parse(raw) as unknown;
    if (parsed && typeof parsed === "object" && "text" in parsed && Object.keys(parsed).length === 1) {
      const txt = (parsed as { text?: unknown }).text;
      return typeof txt === "string" && txt !== "" ? txt : "—";
    }
    return raw;
  } catch {
    return raw;
  }
}
