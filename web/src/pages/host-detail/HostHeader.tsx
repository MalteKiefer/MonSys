import {
  ArrowLeft,
  ExternalLink,
  MoreVertical,
  Radio,
  Tag,
  Unplug,
  Users as UsersIcon,
} from "lucide-react";
import { useEffect, useRef, useState } from "react";
import { Link, useNavigate } from "react-router-dom";

import { formatBytes } from "../../components/Chart";
import { DistroIcon } from "../../components/icons/DistroIcon";
import { ServiceBadges } from "../../components/icons/ServiceIcon";
import { Panel, StatusPill } from "../../components/ui";
import { hostDisplay } from "../../lib/utils";
import { HostDetail as HostDetailT } from "../../lib/types";

// HostHeader is the chrome shared across every sub-route. It deliberately
// stays *display-only* — interactive flows that mutate host state (tag edit,
// group membership) live in their own pages so the header doesn't bloat with
// drawer machinery. The kebab menu links to those flows.
export function HostHeader({ detail }: { detail: HostDetailT }) {
  const h = detail.host;
  return (
    <Panel className="overflow-hidden">
      <div className="p-5">
        <div className="flex items-start justify-between gap-4">
          <div className="min-w-0">
            <Link to="/hosts" className="inline-flex items-center gap-1 text-xs text-fg-subtle hover:text-fg">
              <ArrowLeft className="h-3 w-3" /> Hosts
            </Link>
            <h1 className="mt-1.5 flex items-center gap-2.5 text-xl font-semibold tracking-tight">
              <DistroIcon family={h.distro_family} size={22} />
              <span className="truncate">{hostDisplay(h)}</span>
              <StatusPill status={h.status}>{h.status}</StatusPill>
            </h1>
            <p className="mt-1 text-sm text-fg-muted">
              {h.distro} · {h.arch} · {h.cpu_cores} cores · {formatBytes(h.ram_total_bytes)} RAM ·
              agent <span className="font-mono text-xs">{h.agent_version}</span>
            </p>
            {h.services && h.services.length > 0 && (
              <div className="mt-2"><ServiceBadges services={h.services} max={12} /></div>
            )}
          </div>
          <div className="flex items-start gap-2">
            <div className="text-right text-xs text-fg-subtle">
              <p>last_seen: <span className="text-fg-muted">{relativeTime(h.last_seen_at)}</span></p>
              {h.status_since && <p>since: <span className="text-fg-muted">{relativeTime(h.status_since)}</span></p>}
              <p className="mt-1 font-mono text-[10px] text-fg-subtle">{h.id}</p>
            </div>
            <ActionMenu hostId={h.id} />
          </div>
        </div>
      </div>
    </Panel>
  );
}

// ---- Action menu --------------------------------------------------------

function ActionMenu({ hostId }: { hostId: string }) {
  const [open, setOpen] = useState(false);
  const ref = useRef<HTMLDivElement>(null);
  const navigate = useNavigate();

  // Close on outside click. Mirrors the AdminMenu pattern in App.tsx so the
  // dropdown feels consistent with the rest of the chrome.
  useEffect(() => {
    if (!open) return;
    function onDoc(e: MouseEvent) {
      if (ref.current && !ref.current.contains(e.target as Node)) setOpen(false);
    }
    document.addEventListener("mousedown", onDoc);
    return () => document.removeEventListener("mousedown", onDoc);
  }, [open]);

  // Close on Escape so keyboard users aren't trapped.
  useEffect(() => {
    if (!open) return;
    function onKey(e: KeyboardEvent) {
      if (e.key === "Escape") setOpen(false);
    }
    window.addEventListener("keydown", onKey);
    return () => window.removeEventListener("keydown", onKey);
  }, [open]);

  function close() { setOpen(false); }

  return (
    <div className="relative" ref={ref}>
      <button
        type="button"
        aria-label="Host actions"
        aria-haspopup="menu"
        aria-expanded={open}
        onClick={() => setOpen((v) => !v)}
        className="rounded-md border border-border bg-panel p-1.5 text-fg-muted transition-colors hover:bg-panel-2 hover:text-fg"
      >
        <MoreVertical className="h-4 w-4" />
      </button>
      {open && (
        <div
          role="menu"
          className="absolute right-0 z-20 mt-1.5 min-w-[200px] overflow-hidden rounded-md border border-border bg-panel shadow-panel-strong"
        >
          <MenuItem
            icon={Tag}
            label="Edit tags"
            // Tag editing lives inline on the Overview pane — the action menu
            // routes the user there rather than duplicating the editor in a
            // popover.
            onClick={() => { navigate(`/hosts/${hostId}`); close(); }}
          />
          <MenuItem
            icon={UsersIcon}
            label="Edit groups"
            // Group membership is admin-scoped; deep-link to the admin page
            // where groups are managed. Non-admins still see the link but
            // the route is gated by RequireAdmin.
            onClick={() => { navigate(`/admin/groups`); close(); }}
          />
          <MenuItem
            icon={Radio}
            label="Open in monitors"
            onClick={() => { navigate(`/monitors?host_id=${hostId}`); close(); }}
            trailing={<ExternalLink className="h-3 w-3 text-fg-subtle" />}
          />
          <div className="my-1 h-px bg-border" />
          <MenuItem
            icon={Unplug}
            label="Revoke agent"
            // Disabled until the API endpoint lands. Keeping the menu slot
            // visible so future wiring is a one-line change instead of a
            // chrome shuffle.
            disabled
            title="Not yet implemented"
          />
        </div>
      )}
    </div>
  );
}

function MenuItem({
  icon: Icon,
  label,
  onClick,
  trailing,
  disabled,
  title,
}: {
  icon: React.ComponentType<{ className?: string }>;
  label: string;
  onClick?: () => void;
  trailing?: React.ReactNode;
  disabled?: boolean;
  title?: string;
}) {
  return (
    <button
      type="button"
      role="menuitem"
      onClick={disabled ? undefined : onClick}
      disabled={disabled}
      title={title}
      className={[
        "flex w-full items-center gap-2 px-3 py-2 text-left text-sm transition-colors",
        disabled
          ? "cursor-not-allowed text-fg-subtle"
          : "text-fg-muted hover:bg-panel-2 hover:text-fg",
      ].join(" ")}
    >
      <Icon className="h-3.5 w-3.5" />
      <span className="flex-1">{label}</span>
      {trailing}
    </button>
  );
}

// ---- helpers -------------------------------------------------------------

function relativeTime(iso: string): string {
  const t = new Date(iso).getTime();
  if (Number.isNaN(t)) return iso;
  const diff = (Date.now() - t) / 1000;
  if (diff < 60) return `${Math.round(diff)}s ago`;
  if (diff < 3600) return `${Math.round(diff / 60)}m ago`;
  if (diff < 86400) return `${Math.round(diff / 3600)}h ago`;
  return `${Math.round(diff / 86400)}d ago`;
}
