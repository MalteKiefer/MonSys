import { AlertTriangle, Bell, History } from "lucide-react";
import { NavLink } from "react-router-dom";

import { useT } from "../../i18n/useT";
import { useAuth } from "../../lib/auth";

// Sub-nav rendered at the top of every Notifications* page. Uses NavLinks so
// the active highlight comes "for free" from react-router. Visually mirrors
// the inline pill-tab style the monolith used (`<Tabs>` in components/ui is
// value/onChange-shaped; this one is purely route-driven, hence inlined).
//
// The Rules tab is admin-only; channels and alerts are visible to anyone
// signed in. The page itself enforces the underlying permission semantics.
export function NotificationsTabs() {
  const { t } = useT(["notifications", "common"]);
  const user = useAuth((s) => s.user);
  const isAdmin = user?.role === "admin";

  const items: Array<{
    to: string;
    label: string;
    icon: typeof Bell;
    end?: boolean;
    visible: boolean;
  }> = [
    { to: "/notifications", label: t("notifications:tabs.rules"), icon: AlertTriangle, end: true, visible: isAdmin },
    { to: "/notifications/channels", label: t("notifications:tabs.channels"), icon: Bell, visible: true },
    { to: "/notifications/alerts", label: t("notifications:tabs.alerts"), icon: History, visible: true },
  ];

  return (
    <div
      role="tablist"
      className="inline-flex rounded-md border border-border bg-panel p-0.5"
    >
      {items
        .filter((i) => i.visible)
        .map(({ to, label, icon: Icon, end }) => (
          <NavLink
            key={to}
            to={to}
            end={end}
            role="tab"
            className={({ isActive }) =>
              `inline-flex items-center gap-1.5 rounded px-2.5 py-1 text-xs font-medium transition-colors duration-150 ${
                isActive
                  ? "bg-panel-2 text-fg shadow-panel"
                  : "text-fg-subtle hover:text-fg"
              }`
            }
          >
            <Icon className="h-3.5 w-3.5" />
            {label}
          </NavLink>
        ))}
    </div>
  );
}
