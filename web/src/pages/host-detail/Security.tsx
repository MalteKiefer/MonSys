import { ShieldCheck } from "lucide-react";

import {
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
} from "../../components/ui";
import { useT } from "../../i18n/useT";
import type {
  CrowdsecDecision,
  Fail2banJailInfo,
  FirewallStatus,
  HostSecurity,
} from "../../lib/types";

import { useHostDetail } from "./HostLayout";

type TFn = (key: string, opts?: Record<string, unknown>) => string;

// Security tab: firewalls + fail2ban jails + CrowdSec decisions for the host.
// All three sections live in one panel because operators usually scan posture
// holistically (one quiet glance) rather than digging into a single source.
export function Security() {
  const { security, securityLoading } = useHostDetail();
  const { t } = useT(["hostDetail", "common"]);
  return (
    <Panel>
      <PanelHeader>
        <div className="flex items-center gap-2">
          <ShieldCheck className="h-4 w-4 text-fg-muted" />
          <h3 className="text-sm font-semibold">{t("hostDetail:security.posture")}</h3>
        </div>
      </PanelHeader>
      <PanelBody><SecurityPanel data={security} loading={securityLoading} /></PanelBody>
    </Panel>
  );
}

function SecurityPanel({ data, loading }: { data?: HostSecurity; loading: boolean }) {
  const { t } = useT(["hostDetail", "common"]);
  if (loading || !data) return <Skeleton className="h-32" />;

  const firewalls = data.firewalls ?? [];
  const cs = data.crowdsec ?? [];
  const csByType = cs.reduce<Record<string, number>>((acc, d) => {
    const k = (d.type || "unknown").toLowerCase();
    acc[k] = (acc[k] ?? 0) + 1;
    return acc;
  }, {});

  return (
    <div className="space-y-6">
      <div className="grid gap-5 md:grid-cols-2">
        <div>
          <h4 className="mb-2 text-[11px] font-semibold uppercase tracking-wider text-fg-subtle">{t("hostDetail:security.firewalls")}</h4>
          {firewalls.length === 0 ? <p className="text-sm text-fg-subtle">{t("hostDetail:security.noFirewalls")}</p> : (
            <ul className="space-y-3 text-sm">
              {firewalls.map((f: FirewallStatus) => (
                <li key={f.engine} className="rounded-md border border-border bg-panel-2 p-3">
                  <div className="flex items-center gap-2">
                    <StatusPill status={f.active ? "ok" : "unknown"}>{f.engine}</StatusPill>
                    <span className="text-fg-muted">{t("hostDetail:security.rulesCount", { count: f.rule_count })}</span>
                  </div>
                  <div className="mt-2 grid grid-cols-3 gap-1 font-mono text-[11px] text-fg-subtle">
                    <span title={t("hostDetail:security.policyInTitle")}>{t("hostDetail:security.policyInLabel")} <span className="text-fg-muted">{f.default_input || "—"}</span></span>
                    <span title={t("hostDetail:security.policyFwdTitle")}>{t("hostDetail:security.policyFwdLabel")} <span className="text-fg-muted">{f.default_forward || "—"}</span></span>
                    <span title={t("hostDetail:security.policyOutTitle")}>{t("hostDetail:security.policyOutLabel")} <span className="text-fg-muted">{f.default_output || "—"}</span></span>
                  </div>
                  {f.snapshot_excerpt && (
                    <details className="mt-2 group">
                      <summary className="cursor-pointer text-xs text-fg-subtle hover:text-fg">
                        {t("hostDetail:security.rulesetExcerpt", { chars: f.snapshot_excerpt.length })}
                      </summary>
                      <pre className="mt-2 max-h-72 overflow-auto rounded-sm bg-bg p-2 font-mono text-[10px] leading-snug text-fg-muted">
                        {f.snapshot_excerpt}
                      </pre>
                    </details>
                  )}
                </li>
              ))}
            </ul>
          )}
        </div>

        <div>
          <h4 className="mb-2 text-[11px] font-semibold uppercase tracking-wider text-fg-subtle">{t("hostDetail:security.fail2ban")}</h4>
          {!data.fail2ban || data.fail2ban.length === 0 ? <p className="text-sm text-fg-subtle">{t("hostDetail:security.noFail2ban")}</p> : (
            <ul className="space-y-1 text-sm">
              {data.fail2ban.map((j: Fail2banJailInfo) => (
                <li key={j.jail} className="rounded-md border border-border bg-panel-2 px-2 py-1.5 text-fg-muted">
                  <span className="font-mono text-fg">{j.jail}</span>
                  <span className="ml-2">{t("hostDetail:security.bannedSuffix", { banned: j.currently_banned, currentlyFailed: j.currently_failed, totalFailed: j.total_failed })}</span>
                  {j.banned_ips && j.banned_ips.length > 0 && (
                    <details className="mt-1">
                      <summary className="cursor-pointer text-xs text-fg-subtle hover:text-fg">
                        {t("hostDetail:security.ips", { count: j.banned_ips.length })}
                      </summary>
                      <ul className="mt-1 max-h-40 space-y-0.5 overflow-auto font-mono text-[11px] text-fg-muted">
                        {j.banned_ips.map((ip) => <li key={ip}>{ip}</li>)}
                      </ul>
                    </details>
                  )}
                </li>
              ))}
            </ul>
          )}
        </div>
      </div>

      <div>
        <div className="mb-2 flex flex-wrap items-baseline gap-3">
          <h4 className="text-[11px] font-semibold uppercase tracking-wider text-fg-subtle">{t("hostDetail:security.crowdsec")}</h4>
          {cs.length > 0 && (
            <div className="flex flex-wrap gap-1.5 text-[10px] font-mono">
              <span className="rounded-sm bg-panel-2 px-1.5 py-0.5 text-fg-muted">{t("hostDetail:security.activeCount", { count: cs.length })}</span>
              {Object.entries(csByType).map(([type, n]) => (
                <span key={type} className="rounded-sm bg-warn/10 px-1.5 py-0.5 text-warn ring-1 ring-inset ring-warn/30">
                  {type}: {n}
                </span>
              ))}
            </div>
          )}
        </div>
        {cs.length === 0 ? <p className="text-sm text-fg-subtle">{t("hostDetail:security.noDecisions")}</p> : (
          <div className="overflow-x-auto">
          <Table>
            <THead>
              <tr><TH>{t("hostDetail:security.colScope")}</TH><TH>{t("hostDetail:security.colTarget")}</TH><TH>{t("hostDetail:security.colType")}</TH><TH>{t("hostDetail:security.colOrigin")}</TH><TH>{t("hostDetail:security.colReason")}</TH><TH>{t("hostDetail:security.colUntil")}</TH></tr>
            </THead>
            <TBody>
              {cs.map((d: CrowdsecDecision) => (
                <tr key={d.decision_id} className="hover:bg-panel-2">
                  <TD className="font-mono text-xs text-fg-muted">{d.scope || "—"}</TD>
                  <TD className="font-mono text-xs">{d.target || "—"}</TD>
                  <TD>
                    <StatusPill status={d.type === "ban" ? "fail" : "warn"}>{d.type || "?"}</StatusPill>
                  </TD>
                  <TD className="font-mono text-xs text-fg-muted">{d.origin || "—"}</TD>
                  <TD className="text-fg-muted">{d.reason || "—"}</TD>
                  <TD className="text-fg-muted text-xs">{d.until ? relativeTime(d.until, t) : "—"}</TD>
                </tr>
              ))}
            </TBody>
          </Table>
          </div>
        )}
      </div>
    </div>
  );
}

function relativeTime(iso: string, t: TFn): string {
  const ts = new Date(iso).getTime();
  if (Number.isNaN(ts)) return iso;
  const diff = (Date.now() - ts) / 1000;
  if (diff < 60) return t("hostDetail:header.secondsAgo", { count: Math.round(diff) });
  if (diff < 3600) return t("hostDetail:header.minutesAgo", { count: Math.round(diff / 60) });
  if (diff < 86400) return t("hostDetail:header.hoursAgo", { count: Math.round(diff / 3600) });
  return t("hostDetail:header.daysAgo", { count: Math.round(diff / 86400) });
}
