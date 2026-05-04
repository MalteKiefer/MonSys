import { useEffect, useRef, useState, useSyncExternalStore } from "react";
import { useQuery, useQueryClient } from "@tanstack/react-query";
import { Activity, Bell, ChevronDown, ClipboardList, CloudOff, FileJson, FileText, LayoutDashboard, LogOut, Mail, Moon, Network, Package, Radio, Server, Settings, ShieldCheck, Sliders, UserCog, Users } from "lucide-react";
import { Link, Navigate, NavLink, Route, Routes, useLocation } from "react-router-dom";

import { RequireAdmin } from "./components/RequireAdmin";
import { ThemeToggle } from "./components/ThemeToggle";
import { AdminAgentConfig } from "./pages/AdminAgentConfig";
import { AdminAudit } from "./pages/AdminAudit";
import { AdminGroups } from "./pages/AdminGroups";
import { AdminIngests } from "./pages/AdminIngests";
import { AdminLogs } from "./pages/AdminLogs";
import { AdminMail } from "./pages/AdminMail";
import { AdminQuietHours } from "./pages/AdminQuietHours";
import { AdminMonitors } from "./pages/AdminMonitors";
import { AdminNotifications } from "./pages/AdminNotifications";
import { AdminSecurity } from "./pages/AdminSecurity";
import { AdminUsers } from "./pages/AdminUsers";
import { Dashboard } from "./pages/Dashboard";
import { HostDetail } from "./pages/HostDetail";
import { Hosts } from "./pages/Hosts";
import { Login } from "./pages/Login";
import { Packages } from "./pages/Packages";
import { Profile } from "./pages/Profile";
import { Reset } from "./pages/Reset";
import { api } from "./lib/api";
import { useAuth } from "./lib/auth";
import { getConnectionStatus, subscribe as subscribeConnection } from "./lib/connection";
import { CurrentUser } from "./lib/types";

export function App() {
  const token = useAuth((s) => s.token);
  const setSession = useAuth((s) => s.setSession);
  const persistedUser = useAuth((s) => s.user);

  // Refresh /v1/auth/me on first paint so we pick up totp_active changes
  // and admin-toggled role changes without forcing a re-login.
  const me = useQuery({
    queryKey: ["me"],
    queryFn: () => api<CurrentUser>("/v1/auth/me"),
    enabled: !!token,
    refetchOnWindowFocus: false,
  });
  useEffect(() => {
    if (token && me.data && persistedUser) {
      setSession(token, { ...persistedUser, ...me.data });
    }
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [me.data]);

  if (!token) {
    return (
      <Routes>
        <Route path="/login" element={<Login />} />
        <Route path="/reset" element={<Reset />} />
        <Route path="*" element={<Navigate to="/login" replace />} />
      </Routes>
    );
  }

  return (
    <div className="flex h-full flex-col">
      <Header />
      <ConnectionBanner />
      <main className="flex-1 overflow-auto">
        <Routes>
          <Route path="/" element={<Dashboard />} />
          <Route path="/hosts" element={<Hosts />} />
          <Route path="/hosts/:id" element={<HostDetail />} />
          <Route path="/packages" element={<Packages />} />
          <Route path="/profile" element={<Profile />} />
          <Route path="/notifications" element={<AdminNotifications />} />
          <Route path="/admin/notifications" element={<Navigate to="/notifications" replace />} />
          <Route path="/monitors" element={<AdminMonitors />} />
          <Route path="/admin/monitors" element={<Navigate to="/monitors" replace />} />
          <Route path="/admin/groups" element={<RequireAdmin><AdminGroups /></RequireAdmin>} />
          <Route path="/admin/logs" element={<RequireAdmin><AdminLogs /></RequireAdmin>} />
          <Route path="/admin/ingests" element={<RequireAdmin><AdminIngests /></RequireAdmin>} />
          <Route path="/admin/agent-config" element={<RequireAdmin><AdminAgentConfig /></RequireAdmin>} />
          <Route path="/admin/mail" element={<RequireAdmin><AdminMail /></RequireAdmin>} />
          <Route path="/admin/quiet-hours" element={<RequireAdmin><AdminQuietHours /></RequireAdmin>} />
          <Route path="/admin/users" element={<RequireAdmin><AdminUsers /></RequireAdmin>} />
          <Route path="/admin/security" element={<RequireAdmin><AdminSecurity /></RequireAdmin>} />
          <Route path="/admin/audit" element={<RequireAdmin><AdminAudit /></RequireAdmin>} />
          <Route path="/login" element={<Navigate to="/" replace />} />
          <Route path="/reset" element={<Reset />} />
          <Route path="*" element={<Navigate to="/" replace />} />
        </Routes>
      </main>
    </div>
  );
}

function ConnectionBanner() {
  // Subscribe to the external connection store via useSyncExternalStore so the
  // banner re-renders the moment api() reports a failure (or the OS fires
  // 'offline'). The store throttles failures itself — by the time we render
  // status === "lost" we've already crossed the ≥ 2-failures-in-10s threshold.
  const status = useSyncExternalStore(subscribeConnection, getConnectionStatus, getConnectionStatus);
  if (status !== "lost") return null;
  return (
    <div
      role="status"
      aria-live="polite"
      // fixed (not sticky) so it overlaps the page instead of nudging the
      // header / main downward when it appears. z-40 sits above the header
      // (z-30) so it's clearly visible at the top of the viewport.
      className="pointer-events-none fixed inset-x-0 top-0 z-40 flex items-center justify-center gap-2 border-b border-fail/30 bg-fail/10 px-4 py-1.5 text-xs font-medium text-fail ring-1 ring-inset ring-fail/30 backdrop-blur"
    >
      <CloudOff className="h-3.5 w-3.5" aria-hidden />
      <span>Connection to mon-server lost — retrying…</span>
    </div>
  );
}

function Header() {
  const { user, clear } = useAuth();
  const qc = useQueryClient();
  const isAdmin = user?.role === "admin";

  function logout() {
    api<unknown>("/v1/auth/logout", { method: "POST" }).catch(() => {});
    qc.clear();
    clear();
  }

  return (
    <header className="sticky top-0 z-30 flex items-center justify-between border-b border-border bg-bg/85 px-5 py-2.5 backdrop-blur supports-[backdrop-filter]:bg-bg/70">
      <div className="flex items-center gap-6">
        <Link to="/" className="flex items-center gap-2 text-sm font-semibold tracking-tight">
          <Activity className="h-4 w-4 text-accent" />
          <span>mon</span>
        </Link>
        <nav className="flex items-center gap-0.5">
          <NavItem to="/" icon={LayoutDashboard}>Overview</NavItem>
          <NavItem to="/hosts" icon={Server}>Hosts</NavItem>
          <NavItem to="/packages" icon={Package}>Packages</NavItem>
          <NavItem to="/monitors" icon={Radio}>Monitors</NavItem>
          <NavItem to="/notifications" icon={Bell}>Notifications</NavItem>
          <NavItem to="/profile" icon={UserCog}>Profile</NavItem>
          {isAdmin && (
            <>
              <span className="mx-2 h-4 w-px bg-border" aria-hidden />
              <AdminMenu />
            </>
          )}
        </nav>
      </div>
      <div className="flex items-center gap-3 text-sm">
        {user?.totp_active && (
          <span className="hidden rounded-md bg-ok/10 px-1.5 py-0.5 text-[11px] font-medium text-ok ring-1 ring-inset ring-ok/30 sm:inline">
            2FA
          </span>
        )}
        <span className="text-fg-muted">{user?.email}</span>
        <ThemeToggle />
        <button
          onClick={logout}
          aria-label="Sign out"
          className="inline-flex items-center gap-1 rounded-md border border-border bg-panel px-2 py-1 text-xs text-fg-muted transition-colors hover:bg-panel-2 hover:text-fg"
        >
          <LogOut className="h-3.5 w-3.5" />
          <span className="hidden sm:inline">Sign out</span>
        </button>
      </div>
    </header>
  );
}

const ADMIN_ITEMS = [
  { to: "/admin/groups", label: "Groups", icon: Network },
  { to: "/admin/users", label: "Users", icon: Users },
  { to: "/admin/mail", label: "Mail (SMTP)", icon: Mail },
  { to: "/admin/quiet-hours", label: "Quiet hours", icon: Moon },
  { to: "/admin/agent-config", label: "Agent config", icon: Sliders },
  { to: "/admin/logs", label: "Server logs", icon: FileText },
  { to: "/admin/ingests", label: "Agent ingests", icon: FileJson },
  { to: "/admin/security", label: "Security", icon: ShieldCheck },
  { to: "/admin/audit", label: "Audit log", icon: ClipboardList },
];

function AdminMenu() {
  const [open, setOpen] = useState(false);
  const ref = useRef<HTMLDivElement>(null);
  const itemsRef = useRef<Array<HTMLAnchorElement | null>>([]);
  const loc = useLocation();
  const menuId = "admin-menu";

  useEffect(() => {
    setOpen(false);
  }, [loc.pathname]);

  useEffect(() => {
    if (!open) return;
    function onDoc(e: MouseEvent) {
      if (ref.current && !ref.current.contains(e.target as Node)) setOpen(false);
    }
    document.addEventListener("mousedown", onDoc);
    return () => document.removeEventListener("mousedown", onDoc);
  }, [open]);

  useEffect(() => {
    if (!open) return;
    function onKey(e: KeyboardEvent) {
      if (e.key === "Escape") setOpen(false);
    }
    window.addEventListener("keydown", onKey);
    return () => window.removeEventListener("keydown", onKey);
  }, [open]);

  useEffect(() => {
    if (open) itemsRef.current[0]?.focus();
  }, [open]);

  function onItemKeyDown(e: React.KeyboardEvent<HTMLAnchorElement>, idx: number) {
    if (e.key !== "ArrowDown" && e.key !== "ArrowUp") return;
    e.preventDefault();
    const len = ADMIN_ITEMS.length;
    const next = e.key === "ArrowDown" ? (idx + 1) % len : (idx - 1 + len) % len;
    itemsRef.current[next]?.focus();
  }

  const insideAdmin = loc.pathname.startsWith("/admin");
  const activeItem = ADMIN_ITEMS.find((i) => loc.pathname.startsWith(i.to));

  return (
    <div className="relative" ref={ref}>
      <button
        onClick={() => setOpen((v) => !v)}
        aria-haspopup="menu"
        aria-expanded={open}
        aria-controls={menuId}
        className={`inline-flex items-center gap-1.5 rounded-md px-2.5 py-1.5 text-sm transition-colors duration-150 ${
          insideAdmin ? "bg-panel-2 text-fg shadow-panel" : "text-fg-muted hover:bg-panel hover:text-fg"
        }`}
      >
        <ShieldCheck className="h-3.5 w-3.5" />
        Admin
        {activeItem && <span className="ml-1 text-fg-subtle">· {activeItem.label}</span>}
        <ChevronDown className={`h-3.5 w-3.5 transition-transform duration-150 ${open ? "rotate-180" : ""}`} />
      </button>
      {open && (
        <div
          id={menuId}
          role="menu"
          className="absolute right-0 mt-1.5 min-w-[180px] overflow-hidden rounded-md border border-border bg-panel shadow-panel-strong"
        >
          {ADMIN_ITEMS.map(({ to, label, icon: Icon }, idx) => (
            <NavLink
              key={to}
              to={to}
              role="menuitem"
              tabIndex={open ? 0 : -1}
              ref={(el) => { itemsRef.current[idx] = el; }}
              onKeyDown={(e) => onItemKeyDown(e, idx)}
              className={({ isActive }) =>
                `flex items-center gap-2 px-3 py-2 text-sm transition-colors ${
                  isActive
                    ? "bg-panel-2 text-fg"
                    : "text-fg-muted hover:bg-panel-2 hover:text-fg"
                }`
              }
            >
              <Icon className="h-3.5 w-3.5" />
              {label}
            </NavLink>
          ))}
        </div>
      )}
    </div>
  );
}

function NavItem({
  to,
  icon: Icon,
  children,
}: {
  to: string;
  icon: typeof Settings;
  children: React.ReactNode;
}) {
  return (
    <NavLink
      to={to}
      end={to === "/"}
      className={({ isActive }) =>
        `inline-flex items-center gap-1.5 rounded-md px-2.5 py-1.5 text-sm transition-colors duration-150 ${
          isActive
            ? "bg-panel-2 text-fg shadow-panel"
            : "text-fg-muted hover:bg-panel hover:text-fg"
        }`
      }
    >
      <Icon className="h-3.5 w-3.5" />
      {children}
    </NavLink>
  );
}
