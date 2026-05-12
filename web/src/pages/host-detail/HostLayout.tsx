import { useQuery } from "@tanstack/react-query";
import {
  BarChart3,
  Boxes,
  Container,
  HardDrive,
  LayoutGrid,
  Network as NetworkIcon,
  Package,
  ShieldCheck,
  Users as UsersIcon,
} from "lucide-react";
import { ComponentType, createContext, useContext, useMemo } from "react";
import { NavLink, Outlet, useNavigate, useParams } from "react-router-dom";

import { Skeleton } from "../../components/ui";
import { useT } from "../../i18n/useT";
import { api } from "../../lib/api";
import { HostDetail as HostDetailT, HostSecurity } from "../../lib/types";

import { HostHeader } from "./HostHeader";

// HostDetailContext is the shared state every sub-route reads from. Fetching
// /v1/hosts/:id once at the layout level and threading the response through
// context keeps the wire load constant when the user flips tabs — each tab
// would otherwise re-fetch the same blob via its own React Query key.
type HostDetailContextValue = {
  detail: HostDetailT;
  security: HostSecurity | undefined;
  securityLoading: boolean;
  hostId: string;
};

const HostDetailContext = createContext<HostDetailContextValue | null>(null);

export function useHostDetail(): HostDetailContextValue {
  const v = useContext(HostDetailContext);
  if (!v) throw new Error("useHostDetail must be used inside <HostLayout>");
  return v;
}

// Loose UUID v4-ish guard. The server is the source of truth — this is a
// cheap pre-flight to bounce malformed URLs (e.g. "/hosts/foo") back to the
// list rather than firing a doomed API call.
const UUID_RE = /^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$/i;

export function HostLayout() {
  const { id = "" } = useParams<{ id: string }>();
  const navigate = useNavigate();
  const { t } = useT(["hostDetail", "common"]);

  const detail = useQuery({
    queryKey: ["host", id],
    queryFn: () => api<HostDetailT>(`/v1/hosts/${id}`),
    refetchInterval: 30_000,
    enabled: !!id && UUID_RE.test(id),
  });

  // Security is the second cross-cutting fetch — the dedicated tab needs the
  // full payload, but the header may also surface a posture chip in the
  // future, so fetch it once at the layout. It's lightweight and doesn't
  // block the rest of the page.
  const security = useQuery({
    queryKey: ["host-security", id],
    queryFn: () => api<HostSecurity>(`/v1/hosts/${id}/security`),
    enabled: !!id && UUID_RE.test(id),
    refetchInterval: 60_000,
  });

  if (id && !UUID_RE.test(id)) {
    // Synchronous redirect would re-trigger render cycles — defer one frame.
    queueMicrotask(() => navigate("/hosts", { replace: true }));
    return null;
  }

  if (detail.isLoading) {
    return (
      <div className="mx-auto max-w-6xl space-y-4 p-6">
        <Skeleton className="h-44" />
        <Skeleton className="h-10" />
        <Skeleton className="h-72" />
      </div>
    );
  }
  if (detail.error || !detail.data) {
    return <p className="p-6 text-sm text-fail">{(detail.error as Error)?.message ?? t("hostDetail:header.hostNotFound")}</p>;
  }

  const d = detail.data;
  const ctx: HostDetailContextValue = {
    detail: d,
    security: security.data,
    securityLoading: security.isLoading,
    hostId: id,
  };

  return (
    <HostDetailContext.Provider value={ctx}>
      <div className="mx-auto max-w-6xl space-y-5 p-6">
        <HostHeader detail={d} />
        <SubTabs detail={d} />
        <Outlet />
      </div>
    </HostDetailContext.Provider>
  );
}

// ---- Sub-tab nav ---------------------------------------------------------

type SubTab = {
  to: string;
  end?: boolean;
  label: string;
  icon: ComponentType<{ className?: string }>;
  count?: number;
  /** When true, render dimmed to signal "no data" without hiding the link. */
  dim?: boolean;
};

function SubTabs({ detail }: { detail: HostDetailT }) {
  const id = detail.host.id;
  const { t } = useT(["hostDetail", "common"]);
  const items: SubTab[] = useMemo(() => {
    const workloadCount = detail.workloads.length;
    const vmCount = detail.vms.length;
    return [
      { to: `/hosts/${id}`, end: true, label: t("hostDetail:nav.overview"), icon: LayoutGrid },
      { to: `/hosts/${id}/storage`, label: t("hostDetail:nav.storage"), icon: HardDrive, count: detail.disks.length, dim: detail.disks.length === 0 },
      { to: `/hosts/${id}/network`, label: t("hostDetail:nav.network"), icon: NetworkIcon, count: detail.nics.length, dim: detail.nics.length === 0 },
      { to: `/hosts/${id}/workloads`, label: t("hostDetail:nav.workloads"), icon: Container, count: workloadCount, dim: workloadCount === 0 },
      { to: `/hosts/${id}/vms`, label: t("hostDetail:nav.vms"), icon: Boxes, count: vmCount, dim: vmCount === 0 },
      { to: `/hosts/${id}/users`, label: t("hostDetail:nav.users"), icon: UsersIcon, count: detail.users.length, dim: detail.users.length === 0 },
      { to: `/hosts/${id}/security`, label: t("hostDetail:nav.security"), icon: ShieldCheck },
      { to: `/hosts/${id}/packages`, label: t("hostDetail:nav.packages"), icon: Package, count: detail.packages_summary?.installed_count },
      { to: `/hosts/${id}/charts`, label: t("hostDetail:nav.charts"), icon: BarChart3 },
    ];
  }, [detail, id, t]);

  return (
    <div
      role="tablist"
      className="sticky top-header-h z-20 -mx-2 flex gap-1 overflow-x-auto border-b border-border bg-bg/85 px-2 py-1.5 backdrop-blur supports-[backdrop-filter]:bg-bg/70"
    >
      {items.map(({ to, end, label, icon: Icon, count, dim }) => (
        <NavLink
          key={to}
          to={to}
          end={end}
          className={({ isActive }) =>
            [
              "inline-flex shrink-0 items-center gap-1.5 rounded-md px-3 py-1.5 text-sm font-medium transition-colors duration-150",
              isActive
                ? "bg-panel-2 text-fg shadow-panel"
                : dim
                  ? "text-fg-subtle hover:bg-panel hover:text-fg-muted"
                  : "text-fg-muted hover:bg-panel hover:text-fg",
            ].join(" ")
          }
        >
          <Icon className="h-3.5 w-3.5" />
          {label}
          {count !== undefined && (
            <span className="rounded-full bg-border-strong px-1.5 py-0.5 text-[10px] font-mono text-fg-muted">
              {count}
            </span>
          )}
        </NavLink>
      ))}
    </div>
  );
}
