import { CloudOff } from "lucide-react";
import { Suspense, useEffect, useState, useSyncExternalStore } from "react";

import { MobileDrawer } from "./MobileDrawer";
import { Sidebar } from "./Sidebar";
import { TopBar } from "./TopBar";
import { getConnectionStatus, subscribe as subscribeConnection } from "../../lib/connection";
import { useT } from "../../i18n/useT";

// AppShell is the post-auth chrome: connection banner, topbar, sidebar,
// mobile drawer, and the scrollable main slot for the routed page.
//
// The shell is intentionally route-agnostic — it accepts the routed body as
// `children` and never re-mounts on navigation, so collapsing the sidebar or
// toggling the mobile drawer never tears down a page mid-state.
//
// Tablet behaviour: 768-1023px (md only) — we don't have a Tailwind shortcut
// for "md but not lg", so the sidebar's `forceCollapsed` flag is wired via a
// matchMedia query so the visual collapse follows the breakpoint without a
// JS-driven hover overlay (the simple permanent-collapse mode keeps the
// sidebar predictable and avoids fighting touch users on Surface-class
// devices that hit the md range).
function useTabletForceCollapse(): boolean {
  // We only care if the viewport is in [768, 1024). useSyncExternalStore is
  // not strictly necessary here — useState + matchMedia listener works and
  // SSR is irrelevant in this Vite SPA.
  const [forced, setForced] = useState(() => {
    if (typeof window === "undefined") return false;
    return window.matchMedia("(min-width: 768px) and (max-width: 1023.98px)").matches;
  });
  useEffect(() => {
    const mql = window.matchMedia("(min-width: 768px) and (max-width: 1023.98px)");
    const handle = (e: MediaQueryListEvent) => { setForced(e.matches); };
    mql.addEventListener("change", handle);
    return () => { mql.removeEventListener("change", handle); };
  }, []);
  return forced;
}

function ConnectionBanner() {
  const { t } = useT("nav");
  const status = useSyncExternalStore(subscribeConnection, getConnectionStatus, getConnectionStatus);
  if (status !== "lost") return null;
  return (
    <div
      role="status"
      aria-live="polite"
      // sticky at the very top of the document so it pushes the topbar down
      // rather than overlapping it. z-50 keeps it above the topbar (z-30) and
      // any sticky toolbars in pages.
      className="sticky top-0 z-50 flex items-center justify-center gap-2 border-b border-fail/30 bg-fail/10 px-4 py-1.5 text-xs font-medium text-fail ring-1 ring-inset ring-fail/30 backdrop-blur"
    >
      <CloudOff className="h-3.5 w-3.5" aria-hidden />
      <span>{t("appshell.connection_lost")}</span>
    </div>
  );
}

export function AppShell({ children }: { children: React.ReactNode }) {
  const { t } = useT("nav");
  const [drawerOpen, setDrawerOpen] = useState(false);
  const tabletCollapsed = useTabletForceCollapse();

  return (
    <div className="flex h-full min-h-0 flex-col">
      <ConnectionBanner />
      <TopBar onOpenMobile={() => { setDrawerOpen(true); }} />
      <div className="flex min-h-0 flex-1">
        <Sidebar forceCollapsed={tabletCollapsed} />
        <main className="min-w-0 flex-1 overflow-auto">
          <Suspense fallback={<div className="p-6 text-sm text-fg-muted">{t("appshell.loading")}</div>}>
            {children}
          </Suspense>
        </main>
      </div>
      <MobileDrawer open={drawerOpen} onClose={() => { setDrawerOpen(false); }} />
    </div>
  );
}
