import { Users as UsersIcon } from "lucide-react";

import {
  Empty,
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
import type { ObservedUser } from "../../lib/types";

import { useHostDetail } from "./HostLayout";

type TFn = (key: string, opts?: Record<string, unknown>) => string;

// Users tab: observed local accounts on the host.
export function Users() {
  const { detail } = useHostDetail();
  const { t } = useT(["hostDetail", "common"]);
  return (
    <Panel>
      <PanelHeader>
        <div className="flex items-center gap-2">
          <UsersIcon className="h-4 w-4 text-fg-muted" />
          <h3 className="text-sm font-semibold">{t("hostDetail:users.title", { count: detail.users.length })}</h3>
        </div>
      </PanelHeader>
      <PanelBody className="p-0 overflow-x-auto"><UsersTable rows={detail.users} /></PanelBody>
    </Panel>
  );
}

function UsersTable({ rows }: { rows: ObservedUser[] }) {
  const { t } = useT(["hostDetail", "common"]);
  if (rows.length === 0) return <Empty>{t("hostDetail:users.noUsers")}</Empty>;
  return (
    <Table>
      <THead>
        <tr><TH>{t("hostDetail:users.colUser")}</TH><TH>{t("hostDetail:users.colUid")}</TH><TH>{t("hostDetail:users.colShell")}</TH><TH>{t("hostDetail:users.colSudoer")}</TH><TH>{t("hostDetail:users.colSystem")}</TH><TH>{t("hostDetail:users.colLastLogin")}</TH></tr>
      </THead>
      <TBody>
        {rows.map((u) => (
          <tr key={u.username} className="hover:bg-panel-2">
            <TD className="font-mono text-fg">{u.username}</TD>
            <TD className="tabular-nums text-fg-muted">{u.uid}</TD>
            <TD className="font-mono text-xs text-fg-muted">{u.shell || "—"}</TD>
            <TD>{u.is_sudoer ? <StatusPill status="warn">{t("hostDetail:users.sudoBadge")}</StatusPill> : <span className="text-fg-subtle">—</span>}</TD>
            <TD className="text-fg-subtle">{u.is_system ? t("hostDetail:users.yes") : "—"}</TD>
            <TD className="text-fg-muted text-xs">{u.last_login_at ? relativeTime(u.last_login_at, t) : "—"}</TD>
          </tr>
        ))}
      </TBody>
    </Table>
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
