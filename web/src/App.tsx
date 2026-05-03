import { useEffect } from "react";
import { useQuery, useQueryClient } from "@tanstack/react-query";
import { Link, Navigate, NavLink, Route, Routes } from "react-router-dom";

import { AdminSecurity } from "./pages/AdminSecurity";
import { AdminUsers } from "./pages/AdminUsers";
import { Hosts } from "./pages/Hosts";
import { Login } from "./pages/Login";
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
  const linkBase =
    "px-3 py-1.5 text-sm rounded text-zinc-300 hover:text-white hover:bg-zinc-800";
  const linkActive = "bg-zinc-800 text-white";
  const isAdmin = user?.role === "admin";

  function logout() {
    api<unknown>("/v1/auth/logout", { method: "POST" }).catch(() => {});
    qc.clear();
    clear();
  }

  return (
    <header className="flex items-center justify-between border-b border-zinc-800 bg-zinc-900 px-4 py-2">
      <div className="flex items-center gap-4">
        <Link to="/" className="text-sm font-semibold tracking-tight">
          mon
        </Link>
        <nav className="flex items-center gap-1">
          <NavLink to="/" end className={({ isActive }) => `${linkBase} ${isActive ? linkActive : ""}`}>
            Hosts
          </NavLink>
          <NavLink to="/profile" className={({ isActive }) => `${linkBase} ${isActive ? linkActive : ""}`}>
            Profile
          </NavLink>
          {isAdmin && (
            <>
              <NavLink to="/admin/users" className={({ isActive }) => `${linkBase} ${isActive ? linkActive : ""}`}>
                Users
              </NavLink>
              <NavLink to="/admin/security" className={({ isActive }) => `${linkBase} ${isActive ? linkActive : ""}`}>
                Security
              </NavLink>
            </>
          )}
        </nav>
      </div>
      <div className="flex items-center gap-3 text-sm text-zinc-400">
        <span>{user?.email}</span>
        {user?.totp_active && (
          <span className="rounded bg-ok/15 px-2 py-0.5 text-xs text-ok">2FA</span>
        )}
        <button
          onClick={logout}
          className="rounded border border-zinc-700 px-2 py-1 text-xs hover:bg-zinc-800"
        >
          Sign out
        </button>
      </div>
    </header>
  );
}
