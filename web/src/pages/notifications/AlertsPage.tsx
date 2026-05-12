import { keepPreviousData, useQuery } from "@tanstack/react-query";
import { CheckCircle2, XCircle } from "lucide-react";
import { useMemo, useState } from "react";

import { Page } from "../../components/page";
import {
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
} from "../../components/ui";
import { useT } from "../../i18n/useT";
import { api } from "../../lib/api";
import { hostDisplay } from "../../lib/utils";
import type {
  AlertHistoryEntry,
  Host,
  NotificationRule,
} from "../../lib/types";

import { NotificationsTabs } from "./NotificationsTabs";

// /notifications/alerts — recent alert history with per-host filtering.
//
// Dedup-key prefixes that embed a host id as the trailing segment. Other
// alert types (monitor_failed, cert_expiring, security_updates_pending) key
// off a monitor id or are global, so they cannot be matched to a single host
// and are hidden whenever a specific host is selected.
const HOST_SCOPED_DEDUP_PREFIXES = ["host_offline:", "host_login_failed:"] as const;

function severityStatus(s: NotificationRule["severity"]): "ok" | "warn" | "fail" {
  if (s === "info") return "ok";
  if (s === "warning") return "warn";
  return "fail";
}

function useRelTime() {
  const { t } = useT(["notifications", "common"]);
  return (iso: string): string => {
    const ts = new Date(iso).getTime();
    if (Number.isNaN(ts)) return iso;
    const diff = (Date.now() - ts) / 1000;
    if (diff < 60) return t("notifications:alerts.rel_time.seconds_ago", { count: Math.round(diff) });
    if (diff < 3600) return t("notifications:alerts.rel_time.minutes_ago", { count: Math.round(diff / 60) });
    if (diff < 86400) return t("notifications:alerts.rel_time.hours_ago", { count: Math.round(diff / 3600) });
    return t("notifications:alerts.rel_time.days_ago", { count: Math.round(diff / 86400) });
  };
}

export function AlertsPage() {
  const { t } = useT(["notifications", "common"]);
  const relTime = useRelTime();
  const [since, setSince] = useState("24h");
  const [hostFilter, setHostFilter] = useState<string>("");
  // Date.now() in the memo is intentional — the cutoff snapshot is recomputed
  // when the `since` selector changes, not on every clock tick.
  const sinceISO = useMemo(() => {
    const map: Record<string, number> = { "1h": 3600, "24h": 86400, "7d": 604800, "30d": 2592000 };
    const sec = map[since] ?? 86400;
    // eslint-disable-next-line react-hooks/purity
    return new Date(Date.now() - sec * 1000).toISOString();
  }, [since]);

  const list = useQuery({
    queryKey: ["alerts", sinceISO],
    queryFn: () =>
      api<{ alerts: AlertHistoryEntry[] }>(
        `/v1/notifications/alerts?since=${encodeURIComponent(sinceISO)}&limit=200`,
      ),
    placeholderData: keepPreviousData,
    refetchInterval: 30_000,
  });

  const hostsQuery = useQuery({
    queryKey: ["hosts"],
    queryFn: () => api<{ hosts: Host[] }>("/v1/hosts"),
  });

  const allAlerts = useMemo(() => list.data?.alerts ?? [], [list.data]);
  const filteredAlerts = useMemo(() => {
    if (!hostFilter) return allAlerts;
    const suffix = `:${hostFilter}`;
    return allAlerts.filter((a) => {
      const key = a.dedup_key ?? "";
      // Only host-scoped alert types carry a host id in dedup_key. For other
      // types (monitor_failed, cert_expiring, security_updates_pending) we
      // can't tie them to a specific host, so suppress them when filtering.
      const isHostScoped = HOST_SCOPED_DEDUP_PREFIXES.some((p) => key.startsWith(p));
      return isHostScoped && key.endsWith(suffix);
    });
  }, [allAlerts, hostFilter]);

  const sortedHosts = useMemo(() => {
    const hs = [...(hostsQuery.data?.hosts ?? [])];
    hs.sort((a, b) => hostDisplay(a).localeCompare(hostDisplay(b)));
    return hs;
  }, [hostsQuery.data]);

  return (
    <Page
      title={t("notifications:alerts.page_title")}
      subtitle={t("notifications:alerts.page_subtitle")}
      actions={<NotificationsTabs />}
    >
      <Panel>
        <PanelHeader>
          <h3 className="text-sm font-semibold">{t("notifications:alerts.panel_title")}</h3>
          <div className="inline-flex rounded-md border border-border bg-panel p-0.5">
            {(["1h", "24h", "7d", "30d"] as const).map((s) => (
              <button
                key={s}
                onClick={() => { setSince(s); }}
                className={`rounded px-2.5 py-1 text-xs font-medium transition-colors duration-150 ${
                  since === s ? "bg-panel-2 text-fg shadow-panel" : "text-fg-subtle hover:text-fg"
                }`}
              >
                {t(`notifications:alerts.windows.${s}` as const)}
              </button>
            ))}
          </div>
        </PanelHeader>
        <PanelBody className="p-0 overflow-x-auto">
          <div className="flex flex-wrap items-end gap-3 px-5 py-3 border-b border-border">
            <Field label={t("notifications:alerts.filter_label")} hint={t("notifications:alerts.filter_hint")}>
              <select
                value={hostFilter}
                onChange={(e) => { setHostFilter(e.target.value); }}
                className="w-full rounded-md border border-border bg-panel px-3 py-2 text-sm focus:border-accent focus:outline-none md:w-72"
              >
                <option value="">{t("notifications:alerts.all_hosts")}</option>
                {sortedHosts.map((h) => (
                  <option key={h.id} value={h.id}>
                    {hostDisplay(h)}
                  </option>
                ))}
              </select>
            </Field>
            <p className="pb-2 text-xs text-fg-subtle tabular-nums">
              {hostFilter && allAlerts.length !== filteredAlerts.length
                ? t("notifications:alerts.count_filtered", { filtered: filteredAlerts.length, total: allAlerts.length })
                : t("notifications:alerts.count_total", { count: allAlerts.length })}
            </p>
          </div>
          {list.isLoading ? (
            <p className="px-5 py-4 text-sm text-fg-subtle">{t("common:actions.loading")}</p>
          ) : filteredAlerts.length === 0 ? (
            <p className="px-5 py-8 text-center text-sm text-fg-subtle">
              {allAlerts.length === 0
                ? t("notifications:alerts.empty_window")
                : t("notifications:alerts.empty_filter")}
            </p>
          ) : (
            <Table>
              <THead>
                <tr>
                  <TH>{t("notifications:alerts.table.when")}</TH>
                  <TH>{t("notifications:alerts.table.severity")}</TH>
                  <TH>{t("notifications:alerts.table.rule")}</TH>
                  <TH>{t("notifications:alerts.table.subject")}</TH>
                  <TH>{t("notifications:alerts.table.delivered")}</TH>
                </tr>
              </THead>
              <TBody>
                {filteredAlerts.map((a) => {
                  const errorCount = Object.keys(a.delivery_errors ?? {}).length;
                  return (
                    <tr key={a.id} className="hover:bg-panel-2">
                      <TD className="font-mono text-xs text-fg-muted">{relTime(a.at)}</TD>
                      <TD>
                        <StatusPill status={severityStatus(a.severity)}>
                          {a.severity}
                        </StatusPill>
                      </TD>
                      <TD className="text-fg-muted">{a.rule_name}</TD>
                      <TD>{a.subject}</TD>
                      <TD className="text-fg-muted">
                        {a.delivered_to.length > 0 ? (
                          <span className="inline-flex items-center gap-1 text-ok">
                            <CheckCircle2 className="h-3.5 w-3.5" />
                            {a.delivered_to.length}
                          </span>
                        ) : null}
                        {errorCount > 0 && (
                          <span className="ml-2 inline-flex items-center gap-1 text-fail">
                            <XCircle className="h-3.5 w-3.5" />
                            {errorCount}
                          </span>
                        )}
                      </TD>
                    </tr>
                  );
                })}
              </TBody>
            </Table>
          )}
        </PanelBody>
      </Panel>
    </Page>
  );
}
