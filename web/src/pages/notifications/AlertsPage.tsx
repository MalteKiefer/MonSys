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
import { api } from "../../lib/api";
import { hostDisplay } from "../../lib/utils";
import {
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

function relTime(iso: string): string {
  const t = new Date(iso).getTime();
  if (Number.isNaN(t)) return iso;
  const diff = (Date.now() - t) / 1000;
  if (diff < 60) return `${Math.round(diff)}s ago`;
  if (diff < 3600) return `${Math.round(diff / 60)}m ago`;
  if (diff < 86400) return `${Math.round(diff / 3600)}h ago`;
  return `${Math.round(diff / 86400)}d ago`;
}

export function AlertsPage() {
  const [since, setSince] = useState("24h");
  const [hostFilter, setHostFilter] = useState<string>("");
  const sinceISO = useMemo(() => {
    const map: Record<string, number> = { "1h": 3600, "24h": 86400, "7d": 604800, "30d": 2592000 };
    const sec = map[since] ?? 86400;
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

  const allAlerts = list.data?.alerts ?? [];
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
      title="Alert history"
      subtitle="Recent alerts delivered through the rules + channels above."
      actions={<NotificationsTabs />}
    >
      <Panel>
        <PanelHeader>
          <h3 className="text-sm font-semibold">Alert history</h3>
          <div className="inline-flex rounded-md border border-border bg-panel p-0.5">
            {(["1h", "24h", "7d", "30d"] as const).map((s) => (
              <button
                key={s}
                onClick={() => setSince(s)}
                className={`rounded px-2.5 py-1 text-xs font-medium transition-colors duration-150 ${
                  since === s ? "bg-panel-2 text-fg shadow-panel" : "text-fg-subtle hover:text-fg"
                }`}
              >
                {s}
              </button>
            ))}
          </div>
        </PanelHeader>
        <PanelBody className="p-0 overflow-x-auto">
          <div className="flex flex-wrap items-end gap-3 px-5 py-3 border-b border-border">
            <Field label="Filter by host" hint="Only host-scoped alerts (offline, login-failed) will match.">
              <select
                value={hostFilter}
                onChange={(e) => setHostFilter(e.target.value)}
                className="w-full rounded-md border border-border bg-panel px-3 py-2 text-sm focus:border-accent focus:outline-none md:w-72"
              >
                <option value="">All hosts</option>
                {sortedHosts.map((h) => (
                  <option key={h.id} value={h.id}>
                    {hostDisplay(h)}
                  </option>
                ))}
              </select>
            </Field>
            <p className="pb-2 text-xs text-fg-subtle tabular-nums">
              {hostFilter && allAlerts.length !== filteredAlerts.length
                ? `${filteredAlerts.length} of ${allAlerts.length}`
                : `${allAlerts.length} alert${allAlerts.length === 1 ? "" : "s"}`}
            </p>
          </div>
          {list.isLoading ? (
            <p className="px-5 py-4 text-sm text-fg-subtle">Loading…</p>
          ) : filteredAlerts.length === 0 ? (
            <p className="px-5 py-8 text-center text-sm text-fg-subtle">
              {allAlerts.length === 0
                ? "No alerts in this window."
                : "No alerts match the selected host."}
            </p>
          ) : (
            <Table>
              <THead>
                <tr>
                  <TH>When</TH>
                  <TH>Severity</TH>
                  <TH>Rule</TH>
                  <TH>Subject</TH>
                  <TH>Delivered</TH>
                </tr>
              </THead>
              <TBody>
                {filteredAlerts.map((a) => {
                  const errorCount = Object.keys(a.delivery_errors ?? {}).length;
                  return (
                    <tr key={a.id} className="hover:bg-panel-2">
                      <TD className="font-mono text-xs text-fg-muted">{relTime(a.at)}</TD>
                      <TD>
                        <StatusPill status={severityStatus(a.severity as NotificationRule["severity"])}>
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
