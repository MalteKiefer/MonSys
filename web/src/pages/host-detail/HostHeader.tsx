import { useMutation, useQueryClient } from "@tanstack/react-query";
import {
  ArrowLeft,
  ExternalLink,
  MoreVertical,
  Radio,
  Tag,
  Trash2,
  Unplug,
  Users as UsersIcon,
} from "lucide-react";
import { useEffect, useRef, useState } from "react";
import { Link, useNavigate } from "react-router-dom";

import { formatBytes } from "../../components/chart-utils";
import { DistroIcon } from "../../components/icons/DistroIcon";
import { ServiceBadges } from "../../components/icons/ServiceIcon";
import { Panel, StatusPill } from "../../components/ui";
import { useT } from "../../i18n/useT";
import { ApiError, api } from "../../lib/api";
import { useAuth } from "../../lib/auth";
import { hostDisplay } from "../../lib/utils";
import type { HostDetail as HostDetailT } from "../../lib/types";

// HostHeader is the chrome shared across every sub-route. It deliberately
// stays *display-only* — interactive flows that mutate host state (tag edit,
// group membership) live in their own pages so the header doesn't bloat with
// drawer machinery. The kebab menu links to those flows.
export function HostHeader({ detail }: { detail: HostDetailT }) {
  const h = detail.host;
  const { t } = useT(["hostDetail", "common"]);
  return (
    <Panel className="overflow-hidden">
      <div className="p-5">
        <div className="flex items-start justify-between gap-4">
          <div className="min-w-0">
            <Link to="/hosts" className="inline-flex items-center gap-1 text-xs text-fg-subtle hover:text-fg">
              <ArrowLeft className="h-3 w-3" /> {t("hostDetail:header.backToHosts")}
            </Link>
            <h1 className="mt-1.5 flex items-center gap-2.5 text-xl font-semibold tracking-tight">
              <DistroIcon family={h.distro_family} size={22} />
              <span className="truncate">{hostDisplay(h)}</span>
              <StatusPill status={h.status}>{h.status}</StatusPill>
            </h1>
            <p className="mt-1 text-sm text-fg-muted">
              {t("hostDetail:header.summary", {
                distro: h.distro,
                arch: h.arch,
                cores: h.cpu_cores,
                ram: formatBytes(h.ram_total_bytes),
              })}{" "}
              <span className="font-mono text-xs">{h.agent_version}</span>
            </p>
            {h.services && h.services.length > 0 && (
              <div className="mt-2"><ServiceBadges services={h.services} max={12} /></div>
            )}
          </div>
          <div className="flex items-start gap-2">
            <div className="text-right text-xs text-fg-subtle">
              <p>{t("hostDetail:header.lastSeen")} <span className="text-fg-muted">{relativeTime(h.last_seen_at, t)}</span></p>
              {h.status_since && <p>{t("hostDetail:header.since")} <span className="text-fg-muted">{relativeTime(h.status_since, t)}</span></p>}
              <p className="mt-1 font-mono text-[10px] text-fg-subtle">{h.id}</p>
            </div>
            <ActionMenu hostId={h.id} hostLabel={hostDisplay(h)} />
          </div>
        </div>
      </div>
    </Panel>
  );
}

// ---- Action menu --------------------------------------------------------

function ActionMenu({ hostId, hostLabel }: { hostId: string; hostLabel: string }) {
  const [open, setOpen] = useState(false);
  const ref = useRef<HTMLDivElement>(null);
  const navigate = useNavigate();
  const queryClient = useQueryClient();
  const isAdmin = useAuth((s) => s.user?.role === "admin");
  const { t } = useT(["hostDetail", "common"]);

  // Close on outside click. Mirrors the AdminMenu pattern in App.tsx so the
  // dropdown feels consistent with the rest of the chrome.
  useEffect(() => {
    if (!open) return;
    function onDoc(e: MouseEvent) {
      if (ref.current && !ref.current.contains(e.target as Node)) setOpen(false);
    }
    document.addEventListener("mousedown", onDoc);
    return () => { document.removeEventListener("mousedown", onDoc); };
  }, [open]);

  // Close on Escape so keyboard users aren't trapped.
  useEffect(() => {
    if (!open) return;
    function onKey(e: KeyboardEvent) {
      if (e.key === "Escape") setOpen(false);
    }
    window.addEventListener("keydown", onKey);
    return () => { window.removeEventListener("keydown", onKey); };
  }, [open]);

  function close() { setOpen(false); }

  // Native confirm() matches the AdminUsers destructive-action pattern;
  // these are admin-only and infrequent so a custom modal isn't worth the
  // extra surface yet.
  const reportError = (err: unknown) => {
    const text = err instanceof ApiError ? err.detail : (err as Error).message;
    window.alert(text);
  };

  const invalidateHostQueries = () => {
    void queryClient.invalidateQueries({ queryKey: ["hosts"] });
    void queryClient.invalidateQueries({ queryKey: ["host", hostId] });
  };

  const deactivate = useMutation({
    mutationFn: () => api<unknown>(`/v1/hosts/${hostId}/deactivate`, { method: "POST" }),
    onSuccess: () => {
      invalidateHostQueries();
      close();
    },
    onError: reportError,
  });

  const del = useMutation({
    mutationFn: () => api<unknown>(`/v1/hosts/${hostId}`, { method: "DELETE" }),
    onSuccess: () => {
      invalidateHostQueries();
      close();
      // Host no longer exists — bounce back to the inventory list before
      // child queries try to refetch and 404.
      void navigate("/hosts");
    },
    onError: reportError,
  });

  const onDeactivate = () => {
    if (!window.confirm(t("hostDetail:header.confirmDeactivate", { host: hostLabel }))) {
      return;
    }
    deactivate.mutate();
  };

  const onDelete = () => {
    if (!window.confirm(t("hostDetail:header.confirmDelete", { host: hostLabel }))) {
      return;
    }
    del.mutate();
  };

  return (
    <div className="relative" ref={ref}>
      <button
        type="button"
        aria-label={t("hostDetail:header.hostActionsAria")}
        aria-haspopup="menu"
        aria-expanded={open}
        onClick={() => { setOpen((v) => !v); }}
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
            label={t("hostDetail:header.editTags")}
            // Tag editing lives inline on the Overview pane — the action menu
            // routes the user there rather than duplicating the editor in a
            // popover.
            onClick={() => { void navigate(`/hosts/${hostId}`); close(); }}
          />
          <MenuItem
            icon={UsersIcon}
            label={t("hostDetail:header.editGroups")}
            // Group membership is admin-scoped; deep-link to the admin page
            // where groups are managed. Non-admins still see the link but
            // the route is gated by RequireAdmin.
            onClick={() => { void navigate(`/admin/groups`); close(); }}
          />
          <MenuItem
            icon={Radio}
            label={t("hostDetail:header.openInMonitors")}
            onClick={() => { void navigate(`/monitors?host_id=${hostId}`); close(); }}
            trailing={<ExternalLink className="h-3 w-3 text-fg-subtle" />}
          />
          {isAdmin && (
            <>
              <div className="my-1 h-px bg-border" />
              <MenuItem
                icon={Unplug}
                label={t("hostDetail:header.deactivate")}
                onClick={onDeactivate}
                disabled={deactivate.isPending || del.isPending}
              />
              <MenuItem
                icon={Trash2}
                label={t("hostDetail:header.delete")}
                onClick={onDelete}
                disabled={deactivate.isPending || del.isPending}
                danger
              />
            </>
          )}
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
  danger,
}: {
  icon: React.ComponentType<{ className?: string }>;
  label: string;
  onClick?: () => void;
  trailing?: React.ReactNode;
  disabled?: boolean;
  title?: string;
  danger?: boolean;
}) {
  const baseClass = disabled
    ? "cursor-not-allowed text-fg-subtle"
    : danger
      ? "text-fail hover:bg-fail/10 hover:text-fail"
      : "text-fg-muted hover:bg-panel-2 hover:text-fg";
  return (
    <button
      type="button"
      role="menuitem"
      onClick={disabled ? undefined : onClick}
      disabled={disabled}
      title={title}
      className={["flex w-full items-center gap-2 px-3 py-2 text-left text-sm transition-colors", baseClass].join(" ")}
    >
      <Icon className="h-3.5 w-3.5" />
      <span className="flex-1">{label}</span>
      {trailing}
    </button>
  );
}

// ---- helpers -------------------------------------------------------------

type TFn = (key: string, opts?: Record<string, unknown>) => string;

function relativeTime(iso: string, t: TFn): string {
  const ts = new Date(iso).getTime();
  if (Number.isNaN(ts)) return iso;
  const diff = (Date.now() - ts) / 1000;
  if (diff < 60) return t("hostDetail:header.secondsAgo", { count: Math.round(diff) });
  if (diff < 3600) return t("hostDetail:header.minutesAgo", { count: Math.round(diff / 60) });
  if (diff < 86400) return t("hostDetail:header.hoursAgo", { count: Math.round(diff / 3600) });
  return t("hostDetail:header.daysAgo", { count: Math.round(diff / 86400) });
}
