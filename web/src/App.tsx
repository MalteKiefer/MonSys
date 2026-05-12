import { lazy, useEffect } from "react";
import { useQuery, useQueryClient } from "@tanstack/react-query";
import { Navigate, Route, Routes } from "react-router-dom";

import { AppShell } from "./components/layout/AppShell";
import { CommandPalette } from "./components/CommandPalette";
import { EnforcementGuard } from "./components/EnforcementGuard";
import { RequireAdmin } from "./components/RequireAdmin";
import { AdminMonitors } from "./pages/AdminMonitors";
import { AlertsPage, ChannelsPage, RulesPage } from "./pages/notifications";
import {
  Charts,
  HostLayout,
  Network as HostNetwork,
  Overview,
  Packages as HostPackages,
  Security as HostSecurity,
  Storage,
  Users as HostUsers,
  VMs,
  Workloads,
} from "./pages/host-detail";
import { Dashboard } from "./pages/Dashboard";
import { Hosts } from "./pages/Hosts";
import { Login } from "./pages/Login";
import { Packages } from "./pages/Packages";
import { ConfirmEmail } from "./pages/ConfirmEmail";
import { Profile } from "./pages/Profile";
import { Reset } from "./pages/Reset";
import { DensityProvider } from "./lib/density-store";

// Admin-only pages are lazy-loaded so non-admin users don't pay for them
// on first paint. Each becomes its own chunk and only fetches when the
// route is hit. The user-facing /notifications and /monitors routes still
// import AdminNotifications and AdminMonitors eagerly above because they
// are linked from the main nav.
const AdminAgentConfig = lazy(() =>
  import("./pages/AdminAgentConfig").then((m) => ({ default: m.AdminAgentConfig })),
);
const AdminEnrollments = lazy(() => import("./pages/AdminEnrollments"));
const AdminGroups = lazy(() =>
  import("./pages/AdminGroups").then((m) => ({ default: m.AdminGroups })),
);
// LogsPage consolidates the former /admin/logs, /admin/ingests, and
// /admin/audit views into a single tabbed page. The old paths still resolve
// via Navigate redirects below.
const LogsPage = lazy(() =>
  import("./pages/LogsPage").then((m) => ({ default: m.LogsPage })),
);
const AdminMail = lazy(() =>
  import("./pages/AdminMail").then((m) => ({ default: m.AdminMail })),
);
const AdminQuietHours = lazy(() =>
  import("./pages/AdminQuietHours").then((m) => ({ default: m.AdminQuietHours })),
);
const AdminSecurity = lazy(() =>
  import("./pages/AdminSecurity").then((m) => ({ default: m.AdminSecurity })),
);
const AdminUsers = lazy(() =>
  import("./pages/AdminUsers").then((m) => ({ default: m.AdminUsers })),
);
import i18n from "./i18n";
import { api } from "./lib/api";
import { useAuth } from "./lib/auth";
import type { CurrentUser } from "./lib/types";

export function App() {
  const token = useAuth((s) => s.token);
  const setSession = useAuth((s) => s.setSession);
  const persistedUser = useAuth((s) => s.user);
  const qc = useQueryClient();

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

  // Carry the server-side `language` preference into i18next on every /me
  // refresh. The TopBar switcher writes both to localStorage (immediate paint)
  // and to the server (cross-device); this effect closes the loop on the
  // OTHER device or after a fresh login. "auto" leaves the detector-derived
  // language alone.
  useEffect(() => {
    const lang = me.data?.language;
    if (!lang || lang === "auto") return;
    if (lang === "en" || lang === "de") {
      void i18n.changeLanguage(lang);
    }
  }, [me.data?.language]);

  // When the OS reports we're back online, immediately refetch every active
  // query so the connection banner can clear and stale data gets refreshed
  // without waiting for the user to click somewhere. The connection store
  // already resets its failure window on 'online' — this complements it by
  // generating the actual fetch traffic that flips status back to "ok".
  useEffect(() => {
    function onOnline() {
      void qc.refetchQueries({ type: "active" });
    }
    window.addEventListener("online", onOnline);
    return () => { window.removeEventListener("online", onOnline); };
  }, [qc]);

  if (!token) {
    return (
      <Routes>
        <Route path="/login" element={<Login />} />
        <Route path="/reset" element={<Reset />} />
        {/* Email confirmation must work logged-out too — the request flow
            revokes every session for the user so they normally land here
            with no token. */}
        <Route path="/confirm-email" element={<ConfirmEmail />} />
        <Route path="*" element={<Navigate to="/login" replace />} />
      </Routes>
    );
  }

  return (
    <AppShell>
      <DensityProvider />
      <CommandPalette />
      <EnforcementGuard>
      <Routes>
        <Route path="/" element={<Dashboard />} />
        <Route path="/hosts" element={<Hosts />} />
        <Route path="/hosts/:id" element={<HostLayout />}>
          <Route index element={<Overview />} />
          <Route path="storage" element={<Storage />} />
          <Route path="network" element={<HostNetwork />} />
          <Route path="workloads" element={<Workloads />} />
          <Route path="vms" element={<VMs />} />
          <Route path="users" element={<HostUsers />} />
          <Route path="security" element={<HostSecurity />} />
          <Route path="packages" element={<HostPackages />} />
          <Route path="charts" element={<Charts />} />
        </Route>
        <Route path="/packages" element={<Packages />} />
        <Route path="/profile" element={<Profile />} />
        <Route path="/notifications" element={<RequireAdmin><RulesPage /></RequireAdmin>} />
        <Route path="/notifications/channels" element={<ChannelsPage />} />
        <Route path="/notifications/alerts" element={<AlertsPage />} />
        <Route path="/admin/notifications" element={<Navigate to="/notifications" replace />} />
        <Route path="/monitors" element={<AdminMonitors />} />
        <Route path="/admin/monitors" element={<Navigate to="/monitors" replace />} />
        <Route path="/admin/groups" element={<RequireAdmin><AdminGroups /></RequireAdmin>} />
        <Route path="/admin/logs" element={<RequireAdmin><LogsPage /></RequireAdmin>} />
        {/* Legacy /admin/ingests and /admin/audit deep-links continue to
            work by redirecting to the consolidated /admin/logs page with
            the appropriate tab pre-selected. Wrapped in RequireAdmin so a
            non-admin URL probe is blocked *before* the URL bar changes —
            otherwise the redirect itself reveals the destination tab. */}
        <Route
          path="/admin/ingests"
          element={
            <RequireAdmin>
              <Navigate to="/admin/logs?tab=ingest" replace />
            </RequireAdmin>
          }
        />
        <Route
          path="/admin/audit"
          element={
            <RequireAdmin>
              <Navigate to="/admin/logs?tab=audit" replace />
            </RequireAdmin>
          }
        />
        <Route path="/admin/agent-config" element={<RequireAdmin><AdminAgentConfig /></RequireAdmin>} />
        <Route path="/admin/enrollments" element={<RequireAdmin><AdminEnrollments /></RequireAdmin>} />
        <Route path="/admin/mail" element={<RequireAdmin><AdminMail /></RequireAdmin>} />
        <Route path="/admin/quiet-hours" element={<RequireAdmin><AdminQuietHours /></RequireAdmin>} />
        <Route path="/admin/users" element={<RequireAdmin><AdminUsers /></RequireAdmin>} />
        <Route path="/admin/security" element={<RequireAdmin><AdminSecurity /></RequireAdmin>} />
        <Route path="/login" element={<Navigate to="/" replace />} />
        <Route path="/reset" element={<Reset />} />
        {/* Also routed when logged in: a user who confirms an email change
            in the same tab where they're still authenticated will hit this
            and the page itself calls useAuth().clear() to flush local state. */}
        <Route path="/confirm-email" element={<ConfirmEmail />} />
        <Route path="*" element={<Navigate to="/" replace />} />
      </Routes>
      </EnforcementGuard>
    </AppShell>
  );
}
