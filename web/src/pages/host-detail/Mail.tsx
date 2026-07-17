import { BellPlus, Mail as MailIcon } from "lucide-react";
import { Link } from "react-router-dom";

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
import type { components } from "../../lib/api-types.generated";

import { useHostDetail } from "./HostLayout";

type MailReport = components["schemas"]["MailReport"];
type MailPortCheck = components["schemas"]["MailPortCheck"];
type MailService = components["schemas"]["MailService"];
type PostfixQueue = components["schemas"]["PostfixQueue"];
type RspamdStat = components["schemas"]["RspamdStat"];

// Mail tab: services + queue + rspamd + port/TLS overview for the host's mail
// stack. The data is fetched once at the layout level and threaded through
// context so flipping back to this tab is instant (no re-fetch).
export function Mail() {
  const { mail, mailLoading } = useHostDetail();
  const { t } = useT(["mail", "hostDetail", "common"]);

  return (
    <Panel>
      <PanelHeader>
        <div className="flex items-center gap-2">
          <MailIcon className="h-4 w-4 text-fg-muted" />
          <h3 className="text-sm font-semibold">{t("mail:title")}</h3>
        </div>
      </PanelHeader>
      <PanelBody>
        <MailPanel detected={mail?.detected} report={mail?.report} loading={mailLoading} />
      </PanelBody>
    </Panel>
  );
}

function MailPanel({
  detected,
  report,
  loading,
}: {
  detected: boolean | undefined;
  report: MailReport | undefined;
  loading: boolean;
}) {
  const { t } = useT(["mail", "common"]);

  if (loading || detected === undefined) return <Skeleton className="h-32" />;
  if (!detected) return <p className="text-sm text-fg-subtle">{t("mail:notDetected")}</p>;
  if (!report) return <Skeleton className="h-32" />;

  return (
    <div className="space-y-6">
      <ServicesSection services={report.services ?? []} />
      {report.queue && <QueueSection queue={report.queue} />}
      {report.rspamd && <RspamdSection rspamd={report.rspamd} />}
      <PortsSection ports={report.ports ?? []} />
      <SuggestedAlertsSection />
    </div>
  );
}

function ServicesSection({ services }: { services: MailService[] }) {
  const { t } = useT(["mail"]);
  return (
    <div>
      <h4 className="mb-2 text-[11px] font-semibold uppercase tracking-wider text-fg-subtle">
        {t("mail:services")}
      </h4>
      {services.length === 0 ? (
        <p className="text-sm text-fg-subtle">{t("mail:noServices")}</p>
      ) : (
        <ul className="flex flex-wrap gap-2">
          {services.map((svc: MailService) => (
            <li key={svc.name}>
              <StatusPill status={svc.active ? "ok" : "fail"}>
                {svc.name}
                {svc.sub_state ? ` (${svc.sub_state})` : ""}
              </StatusPill>
            </li>
          ))}
        </ul>
      )}
    </div>
  );
}

function QueueSection({ queue }: { queue: PostfixQueue }) {
  const { t } = useT(["mail"]);
  const stats: { label: string; value: number; warn?: boolean }[] = [
    { label: t("mail:queueActive"), value: queue.active },
    { label: t("mail:queueDeferred"), value: queue.deferred, warn: queue.deferred > 0 },
    { label: t("mail:queueHold"), value: queue.hold, warn: queue.hold > 0 },
    { label: t("mail:queueTotal"), value: queue.total },
  ];
  return (
    <div>
      <h4 className="mb-2 text-[11px] font-semibold uppercase tracking-wider text-fg-subtle">
        {t("mail:queue")}
      </h4>
      <div className="grid grid-cols-2 gap-2 sm:grid-cols-4">
        {stats.map(({ label, value, warn }) => (
          <div key={label} className="rounded-md border border-border bg-panel-2 px-3 py-2">
            <p className="text-[10px] text-fg-subtle">{label}</p>
            <p className={`mt-0.5 font-mono text-base font-semibold ${warn ? "text-warn" : "text-fg"}`}>
              {value}
            </p>
          </div>
        ))}
      </div>
    </div>
  );
}

function RspamdSection({ rspamd }: { rspamd: RspamdStat }) {
  const { t } = useT(["mail"]);
  if (!rspamd.reachable) return null;
  const stats: { label: string; value: number }[] = [
    { label: t("mail:rspamdScanned"), value: rspamd.scanned },
    { label: t("mail:rspamdGreylisted"), value: rspamd.greylisted },
    { label: t("mail:rspamdRejected"), value: rspamd.rejected },
  ];
  return (
    <div>
      <h4 className="mb-2 text-[11px] font-semibold uppercase tracking-wider text-fg-subtle">
        {t("mail:rspamd")}
      </h4>
      <div className="grid grid-cols-2 gap-2 sm:grid-cols-3">
        {stats.map(({ label, value }) => (
          <div key={label} className="rounded-md border border-border bg-panel-2 px-3 py-2">
            <p className="text-[10px] text-fg-subtle">{label}</p>
            <p className="mt-0.5 font-mono text-base font-semibold text-fg">{value}</p>
          </div>
        ))}
      </div>
    </div>
  );
}

function PortsSection({ ports }: { ports: MailPortCheck[] }) {
  const { t } = useT(["mail"]);
  return (
    <div>
      <h4 className="mb-2 text-[11px] font-semibold uppercase tracking-wider text-fg-subtle">
        {t("mail:ports")}
      </h4>
      {ports.length === 0 ? (
        <p className="text-sm text-fg-subtle">{t("mail:noPorts")}</p>
      ) : (
        <div className="overflow-x-auto">
          <Table>
            <THead>
              <tr>
                <TH>{t("mail:colPort")}</TH>
                <TH>{t("mail:colProto")}</TH>
                <TH>{t("mail:colOpen")}</TH>
                <TH>{t("mail:colTls")}</TH>
                <TH>{t("mail:colCertExpiry")}</TH>
              </tr>
            </THead>
            <TBody>
              {ports.map((p: MailPortCheck) => (
                <tr key={`${p.port}-${p.proto}`} className="hover:bg-panel-2">
                  <TD className="font-mono text-xs">{p.port}</TD>
                  <TD className="font-mono text-xs text-fg-muted">{p.proto}</TD>
                  <TD>
                    <StatusPill status={p.open ? "ok" : "unknown"}>
                      {p.open ? t("mail:open") : t("mail:closed")}
                    </StatusPill>
                  </TD>
                  <TD>
                    {p.tls ? (
                      <span className="flex items-center gap-1">
                        <StatusPill status="ok">TLS</StatusPill>
                        {!p.cert_trusted && (
                          <span className="text-[10px] text-warn">{t("mail:selfSigned")}</span>
                        )}
                      </span>
                    ) : (
                      <span className="text-xs text-fg-subtle">—</span>
                    )}
                  </TD>
                  <TD className="text-xs text-fg-muted">
                    {p.cert_not_after ? certExpiryLabel(p.cert_not_after, t) : "—"}
                  </TD>
                </tr>
              ))}
            </TBody>
          </Table>
        </div>
      )}
    </div>
  );
}

function SuggestedAlertsSection() {
  const { t } = useT(["mail"]);

  const suggestions: { labelKey: string; descKey: string }[] = [
    { labelKey: "mail:suggestedAlerts.serviceDown.label", descKey: "mail:suggestedAlerts.serviceDown.desc" },
    { labelKey: "mail:suggestedAlerts.queueDeferred.label", descKey: "mail:suggestedAlerts.queueDeferred.desc" },
  ];

  return (
    <div>
      <h4 className="mb-2 text-[11px] font-semibold uppercase tracking-wider text-fg-subtle">
        {t("mail:suggestedAlerts.title")}
      </h4>
      <p className="mb-3 text-xs text-fg-subtle">{t("mail:suggestedAlerts.hint")}</p>
      <ul className="space-y-2">
        {suggestions.map(({ labelKey, descKey }) => (
          <li key={labelKey}>
            <Link
              to="/notifications"
              className="flex items-start gap-2 rounded-md border border-border bg-panel-2 px-3 py-2 text-left hover:border-accent/50 hover:bg-panel-2/80 transition-colors duration-150"
            >
              <BellPlus className="mt-0.5 h-3.5 w-3.5 shrink-0 text-accent" />
              <span>
                <span className="block text-xs font-medium text-fg">{t(labelKey)}</span>
                <span className="block text-[11px] text-fg-subtle">{t(descKey)}</span>
              </span>
            </Link>
          </li>
        ))}
      </ul>
    </div>
  );
}

type TFn = (key: string, opts?: Record<string, unknown>) => string;

function certExpiryLabel(iso: string, t: TFn): string {
  const ts = new Date(iso).getTime();
  if (Number.isNaN(ts)) return iso;
  const diffMs = ts - Date.now();
  const diffDays = Math.ceil(diffMs / 86_400_000);
  if (diffDays < 0) return t("mail:certExpired");
  if (diffDays === 0) return t("mail:certExpiresToday");
  return t("mail:certExpiresIn", { days: diffDays });
}
