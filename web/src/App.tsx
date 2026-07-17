import { lazy, Suspense, useEffect } from "react";
import { useQuery, useQueryClient } from "@tanstack/react-query";
import { Navigate, Route, Routes } from "react-router-dom";

// The login flow is the only entry point a logged-out user can reach, so
// only Login / Reset / ConfirmEmail are imported eagerly. Every
// authenticated route (Dashboard, host-detail, AdminMonitors,
// notifications, Profile, Packages, the AppShell chrome, etc.) is
// code-split into its own chunk via `lazy()`. This was previously a hot
// drag on LCP on /login: ~400 kB of authed-only JS was parsed before the
// passkey button could paint, even though none of it would run until the
// user signed in. The auth gate below renders only Login routes when
// `token` is null, so the lazy chunks for the authed shell are never
// fetched until the user has a session.
import { Login } from "./pages/Login";
import { ConfirmEmail } from "./pages/ConfirmEmail";
import { Reset } from "./pages/Reset";
import { DensityProvider } from "./lib/density-store";

const AppShell = lazy(() =>
  import("./components/layout/AppShell").then((m) => ({ default: m.AppShell })),
);
const CommandPalette = lazy(() =>
  import("./components/CommandPalette").then((m) => ({ default: m.CommandPalette })),
);
const EnforcementGuard = lazy(() =>
  import("./components/EnforcementGuard").then((m) => ({ default: m.EnforcementGuard })),
);
const RequireAdmin = lazy(() =>
  import("./components/RequireAdmin").then((m) => ({ default: m.RequireAdmin })),
);
const AdminMonitors = lazy(() =>
  import("./pages/AdminMonitors").then((m) => ({ default: m.AdminMonitors })),
);
const AlertsPage = lazy(() =>
  import("./pages/notifications").then((m) => ({ default: m.AlertsPage })),
);
const ChannelsPage = lazy(() =>
  import("./pages/notifications").then((m) => ({ default: m.ChannelsPage })),
);
const RulesPage = lazy(() =>
  import("./pages/notifications").then((m) => ({ default: m.RulesPage })),
);
const Charts = lazy(() =>
  import("./pages/host-detail").then((m) => ({ default: m.Charts })),
);
const HostLayout = lazy(() =>
  import("./pages/host-detail").then((m) => ({ default: m.HostLayout })),
);
const HostNetwork = lazy(() =>
  import("./pages/host-detail").then((m) => ({ default: m.Network })),
);
const Overview = lazy(() =>
  import("./pages/host-detail").then((m) => ({ default: m.Overview })),
);
const HostPackages = lazy(() =>
  import("./pages/host-detail").then((m) => ({ default: m.Packages })),
);
const HostSecurity = lazy(() =>
  import("./pages/host-detail").then((m) => ({ default: m.Security })),
);
const HostMail = lazy(() =>
  import("./pages/host-detail").then((m) => ({ default: m.Mail })),
);
const Storage = lazy(() =>
  import("./pages/host-detail").then((m) => ({ default: m.Storage })),
);
const HostUsers = lazy(() =>
  import("./pages/host-detail").then((m) => ({ default: m.Users })),
);
const VMs = lazy(() =>
  import("./pages/host-detail").then((m) => ({ default: m.VMs })),
);
const Workloads = lazy(() =>
  import("./pages/host-detail").then((m) => ({ default: m.Workloads })),
);
const Dashboard = lazy(() =>
  import("./pages/Dashboard").then((m) => ({ default: m.Dashboard })),
);
const Hosts = lazy(() =>
  import("./pages/Hosts").then((m) => ({ default: m.Hosts })),
);
const Packages = lazy(() =>
  import("./pages/Packages").then((m) => ({ default: m.Packages })),
);
const Profile = lazy(() =>
  import("./pages/Profile").then((m) => ({ default: m.Profile })),
);

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
    <Suspense fallback={<div className="h-full" />}>
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
          <Route path="mail" element={<HostMail />} />
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
    </Suspense>
  );
}
