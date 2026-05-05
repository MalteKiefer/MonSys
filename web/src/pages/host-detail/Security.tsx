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
import {
  CrowdsecDecision,
  Fail2banJailInfo,
  FirewallStatus,
  HostSecurity,
} from "../../lib/types";

import { useHostDetail } from "./HostLayout";

// Security tab: firewalls + fail2ban jails + CrowdSec decisions for the host.
// All three sections live in one panel because operators usually scan posture
// holistically (one quiet glance) rather than digging into a single source.
export function Security() {
  const { security, securityLoading } = useHostDetail();
  return (
    <Panel>
      <PanelHeader>
        <div className="flex items-center gap-2">
          <ShieldCheck className="h-4 w-4 text-fg-muted" />
          <h3 className="text-sm font-semibold">Posture</h3>
        </div>
      </PanelHeader>
      <PanelBody><SecurityPanel data={security} loading={securityLoading} /></PanelBody>
    </Panel>
  );
}

function SecurityPanel({ data, loading }: { data?: HostSecurity; loading: boolean }) {
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
          <h4 className="mb-2 text-[11px] font-semibold uppercase tracking-wider text-fg-subtle">Firewalls</h4>
          {firewalls.length === 0 ? <p className="text-sm text-fg-subtle">None detected.</p> : (
            <ul className="space-y-3 text-sm">
              {firewalls.map((f: FirewallStatus) => (
                <li key={f.engine} className="rounded-md border border-border bg-panel-2 p-3">
                  <div className="flex items-center gap-2">
                    <StatusPill status={f.active ? "ok" : "unknown"}>{f.engine}</StatusPill>
                    <span className="text-fg-muted">{f.rule_count} rules</span>
                  </div>
                  <div className="mt-2 grid grid-cols-3 gap-1 font-mono text-[11px] text-fg-subtle">
                    <span title="default INPUT policy">in: <span className="text-fg-muted">{f.default_input || "—"}</span></span>
                    <span title="default FORWARD policy">fwd: <span className="text-fg-muted">{f.default_forward || "—"}</span></span>
                    <span title="default OUTPUT policy">out: <span className="text-fg-muted">{f.default_output || "—"}</span></span>
                  </div>
                  {f.snapshot_excerpt && (
                    <details className="mt-2 group">
                      <summary className="cursor-pointer text-xs text-fg-subtle hover:text-fg">
                        ruleset excerpt ({f.snapshot_excerpt.length} chars)
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
          <h4 className="mb-2 text-[11px] font-semibold uppercase tracking-wider text-fg-subtle">fail2ban</h4>
          {!data.fail2ban || data.fail2ban.length === 0 ? <p className="text-sm text-fg-subtle">No fail2ban data.</p> : (
            <ul className="space-y-1 text-sm">
              {data.fail2ban.map((j: Fail2banJailInfo) => (
                <li key={j.jail} className="rounded-md border border-border bg-panel-2 px-2 py-1.5 text-fg-muted">
                  <span className="font-mono text-fg">{j.jail}</span>
                  <span className="ml-2">{j.currently_banned} banned · {j.currently_failed}/{j.total_failed} failed</span>
                  {j.banned_ips && j.banned_ips.length > 0 && (
                    <details className="mt-1">
                      <summary className="cursor-pointer text-xs text-fg-subtle hover:text-fg">
                        {j.banned_ips.length} IP{j.banned_ips.length === 1 ? "" : "s"}
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
          <h4 className="text-[11px] font-semibold uppercase tracking-wider text-fg-subtle">CrowdSec</h4>
          {cs.length > 0 && (
            <div className="flex flex-wrap gap-1.5 text-[10px] font-mono">
              <span className="rounded-sm bg-panel-2 px-1.5 py-0.5 text-fg-muted">{cs.length} active</span>
              {Object.entries(csByType).map(([t, n]) => (
                <span key={t} className="rounded-sm bg-warn/10 px-1.5 py-0.5 text-warn ring-1 ring-inset ring-warn/30">
                  {t}: {n}
                </span>
              ))}
            </div>
          )}
        </div>
        {cs.length === 0 ? <p className="text-sm text-fg-subtle">No decisions.</p> : (
          <Table>
            <THead>
              <tr><TH>Scope</TH><TH>Target</TH><TH>Type</TH><TH>Origin</TH><TH>Reason</TH><TH>Until</TH></tr>
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
                  <TD className="text-fg-muted text-xs">{d.until ? relativeTime(d.until) : "—"}</TD>
                </tr>
              ))}
            </TBody>
          </Table>
        )}
      </div>
    </div>
  );
}

function relativeTime(iso: string): string {
  const t = new Date(iso).getTime();
  if (Number.isNaN(t)) return iso;
  const diff = (Date.now() - t) / 1000;
  if (diff < 60) return `${Math.round(diff)}s ago`;
  if (diff < 3600) return `${Math.round(diff / 60)}m ago`;
  if (diff < 86400) return `${Math.round(diff / 3600)}h ago`;
  return `${Math.round(diff / 86400)}d ago`;
}
