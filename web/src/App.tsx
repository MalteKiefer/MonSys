import { useEffect } from "react";
import { useQuery, useQueryClient } from "@tanstack/react-query";
import { Activity, LogOut, Package, Server, Settings, ShieldCheck, UserCog, Users } from "lucide-react";
import { Link, Navigate, NavLink, Route, Routes } from "react-router-dom";

import { AdminSecurity } from "./pages/AdminSecurity";
import { AdminUsers } from "./pages/AdminUsers";
import { HostDetail } from "./pages/HostDetail";
import { Hosts } from "./pages/Hosts";
import { Login } from "./pages/Login";
import { Packages } from "./pages/Packages";
import { Profile } from "./pages/Profile";
import { Reset } from "./pages/Reset";
import { api } from "./lib/api";
import { useAuth } from "./lib/auth";
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
      <main className="flex-1 overflow-auto">
        <Routes>
          <Route path="/" element={<Hosts />} />
          <Route path="/hosts/:id" element={<HostDetail />} />
          <Route path="/packages" element={<Packages />} />
          <Route path="/profile" element={<Profile />} />
          <Route path="/admin/users" element={<AdminUsers />} />
          <Route path="/admin/security" element={<AdminSecurity />} />
          <Route path="/login" element={<Navigate to="/" replace />} />
          <Route path="/reset" element={<Reset />} />
          <Route path="*" element={<Navigate to="/" replace />} />
        </Routes>
      </main>
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
          <NavItem to="/" icon={Server}>Hosts</NavItem>
          <NavItem to="/packages" icon={Package}>Packages</NavItem>
          <NavItem to="/profile" icon={UserCog}>Profile</NavItem>
          {isAdmin && (
            <>
              <span className="mx-2 h-4 w-px bg-border" aria-hidden />
              <NavItem to="/admin/users" icon={Users}>Users</NavItem>
              <NavItem to="/admin/security" icon={ShieldCheck}>Security</NavItem>
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
