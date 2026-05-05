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
import { ObservedUser } from "../../lib/types";

import { useHostDetail } from "./HostLayout";

// Users tab: observed local accounts on the host.
export function Users() {
  const { detail } = useHostDetail();
  return (
    <Panel>
      <PanelHeader>
        <div className="flex items-center gap-2">
          <UsersIcon className="h-4 w-4 text-fg-muted" />
          <h3 className="text-sm font-semibold">Local accounts ({detail.users.length})</h3>
        </div>
      </PanelHeader>
      <PanelBody className="p-0 overflow-x-auto"><UsersTable rows={detail.users} /></PanelBody>
    </Panel>
  );
}

function UsersTable({ rows }: { rows: ObservedUser[] }) {
  if (rows.length === 0) return <Empty>No observed users.</Empty>;
  return (
    <Table>
      <THead>
        <tr><TH>User</TH><TH>UID</TH><TH>Shell</TH><TH>Sudoer</TH><TH>System</TH><TH>Last login</TH></tr>
      </THead>
      <TBody>
        {rows.map((u) => (
          <tr key={u.username} className="hover:bg-panel-2">
            <TD className="font-mono text-fg">{u.username}</TD>
            <TD className="tabular-nums text-fg-muted">{u.uid}</TD>
            <TD className="font-mono text-xs text-fg-muted">{u.shell || "—"}</TD>
            <TD>{u.is_sudoer ? <StatusPill status="warn">sudo</StatusPill> : <span className="text-fg-subtle">—</span>}</TD>
            <TD className="text-fg-subtle">{u.is_system ? "yes" : "—"}</TD>
            <TD className="text-fg-muted text-xs">{u.last_login_at ? relativeTime(u.last_login_at) : "—"}</TD>
          </tr>
        ))}
      </TBody>
    </Table>
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
