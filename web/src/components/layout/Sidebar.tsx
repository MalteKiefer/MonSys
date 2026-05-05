import {
  Activity,
  AlertTriangle,
  Bell,
  ChevronDown,
  Cog,
  FileSearch,
  LayoutDashboard,
  MessageSquare,
  Network,
  Package,
  PanelLeftClose,
  PanelLeftOpen,
  Radio,
  Server,
  ShieldCheck,
  Stethoscope,
  UserCog,
  Users,
} from "lucide-react";
import { useEffect, useMemo, useState } from "react";
import { NavLink, useLocation } from "react-router-dom";

import { useAuth } from "../../lib/auth";
import { useLayoutStore } from "../../lib/layout-store";

// Shared icon-bearing entry shape used by Sidebar, MobileDrawer, and the
// admin sub-group accordions. We keep the shape narrow on purpose: every
// item has a route (even disabled ones still need a stable key — but those
// are intentionally excluded for now per the spec).
type NavItem = {
  to: string;
  label: string;
  icon: typeof LayoutDashboard;
  end?: boolean;
};

type NavGroup = {
  label: string;
  items: NavItem[];
};

type AdminSubGroup = {
  label: string;
  icon: typeof LayoutDashboard;
  items: NavItem[];
};

// TODO(phase-d): wire /notifications/alerts (AlertTriangle) and
// /notifications/channels (MessageSquare) into NOTIFICATION_GROUP.items
// once the corresponding pages land. They are intentionally omitted today
// because their routes are not yet mounted in App.tsx.
export const NOTIFICATION_GROUP: NavGroup = {
  label: "Notifications",
  items: [
    { to: "/notifications", label: "Rules", icon: Bell, end: true },
  ],
};

export const WORKLOADS_GROUP: NavGroup = {
  label: "Workloads",
  items: [
    { to: "/", label: "Overview", icon: LayoutDashboard, end: true },
    { to: "/hosts", label: "Hosts", icon: Server },
    { to: "/packages", label: "Packages", icon: Package },
    { to: "/monitors", label: "Monitors", icon: Radio },
  ],
};

export const ACCOUNT_GROUP: NavGroup = {
  label: "Account",
  items: [{ to: "/profile", label: "Profile", icon: UserCog }],
};

export const ADMIN_SUBGROUPS: AdminSubGroup[] = [
  {
    label: "Fleet",
    icon: Network,
    items: [
      { to: "/admin/groups", label: "Groups", icon: Network },
      { to: "/admin/agent-config", label: "Agent config", icon: Cog },
      { to: "/admin/enrollments", label: "Enrollments", icon: Server },
    ],
  },
  {
    label: "Identity",
    icon: Users,
    items: [
      { to: "/admin/users", label: "Users", icon: Users },
      { to: "/admin/mail", label: "Mail (SMTP)", icon: MessageSquare },
    ],
  },
  {
    label: "Operations",
    icon: Activity,
    items: [
      { to: "/admin/quiet-hours", label: "Quiet hours", icon: Bell },
      { to: "/admin/security", label: "Security", icon: ShieldCheck },
    ],
  },
  {
    label: "Diagnostics",
    icon: Stethoscope,
    items: [
      { to: "/admin/logs", label: "Server logs", icon: FileSearch },
      { to: "/admin/ingests", label: "Agent ingests", icon: FileSearch },
      { to: "/admin/audit", label: "Audit log", icon: AlertTriangle },
    ],
  },
];

// Lightweight visual helper so MobileDrawer can re-use the same primitive
// without inheriting the desktop's collapsed-icon-only rules.
type LinkProps = {
  item: NavItem;
  collapsed: boolean;
  onNavigate?: () => void;
};

function NavItemLink({ item, collapsed, onNavigate }: LinkProps) {
  const Icon = item.icon;
  return (
    <NavLink
      to={item.to}
      end={item.end}
      onClick={onNavigate}
      title={collapsed ? item.label : undefined}
      aria-label={collapsed ? item.label : undefined}
      className={({ isActive }) =>
        [
          "group flex items-center gap-2.5 rounded-md py-1.5 text-sm transition-colors duration-150",
          collapsed ? "justify-center px-0 mx-1.5" : "px-2.5",
          isActive
            ? "bg-panel-2 text-fg shadow-panel"
            : "text-fg-muted hover:bg-panel hover:text-fg",
        ].join(" ")
      }
    >
      <Icon className="h-4 w-4 shrink-0" aria-hidden />
      {!collapsed && <span className="truncate">{item.label}</span>}
    </NavLink>
  );
}

function GroupLabel({ children, collapsed }: { children: React.ReactNode; collapsed: boolean }) {
  if (collapsed) {
    // Visual divider in collapsed mode — a thin rule keeps icon clusters
    // visually grouped without leaking the heading text.
    return <div className="mx-3 my-2 h-px bg-border" aria-hidden />;
  }
  return (
    <div className="px-3 pb-1 pt-3 text-[10px] font-semibold uppercase tracking-wider text-fg-subtle">
      {children}
    </div>
  );
}

type AdminAccordionProps = {
  group: AdminSubGroup;
  collapsed: boolean;
  pathname: string;
  onNavigate?: () => void;
};

function AdminAccordion({ group, collapsed, pathname, onNavigate }: AdminAccordionProps) {
  const childActive = useMemo(
    () => group.items.some((i) => pathname === i.to || pathname.startsWith(i.to + "/")),
    [group.items, pathname],
  );
  const [open, setOpen] = useState(childActive);

  // Re-open the accordion whenever route navigation lands inside it. Without
  // this the user could collapse it manually and then click a nav link from
  // a sub-route — the active item would render hidden.
  useEffect(() => {
    if (childActive) setOpen(true);
  }, [childActive]);

  const Icon = group.icon;

  if (collapsed) {
    // In collapsed sidebars the accordion header is irrelevant — just render
    // the leaf items as icon buttons. This preserves direct deep-link access.
    return (
      <div className="space-y-0.5">
        {group.items.map((item) => (
          <NavItemLink key={item.to} item={item} collapsed onNavigate={onNavigate} />
        ))}
      </div>
    );
  }

  const buttonId = `admin-acc-${group.label.toLowerCase()}`;
  const panelId = `${buttonId}-panel`;

  return (
    <div>
      <button
        type="button"
        id={buttonId}
        aria-expanded={open}
        aria-controls={panelId}
        onClick={() => setOpen((v) => !v)}
        className={[
          "flex w-full items-center gap-2.5 rounded-md px-2.5 py-1.5 text-sm transition-colors duration-150",
          childActive ? "text-fg" : "text-fg-muted hover:bg-panel hover:text-fg",
        ].join(" ")}
      >
        <Icon className="h-4 w-4 shrink-0" aria-hidden />
        <span className="flex-1 truncate text-left">{group.label}</span>
        <ChevronDown
          className={["h-3.5 w-3.5 transition-transform duration-150", open ? "rotate-0" : "-rotate-90"].join(" ")}
          aria-hidden
        />
      </button>
      {open && (
        <div id={panelId} role="region" aria-labelledby={buttonId} className="ml-3 mt-0.5 space-y-0.5 border-l border-border pl-2">
          {group.items.map((item) => (
            <NavItemLink key={item.to} item={item} collapsed={false} onNavigate={onNavigate} />
          ))}
        </div>
      )}
    </div>
  );
}

// SidebarNav renders the nav body without the toggle header — used by both
// the desktop sidebar and the mobile drawer. The mobile drawer always wants
// the expanded layout, so it passes collapsed=false unconditionally.
export function SidebarNav({
  collapsed,
  onNavigate,
}: {
  collapsed: boolean;
  onNavigate?: () => void;
}) {
  const user = useAuth((s) => s.user);
  const isAdmin = user?.role === "admin";
  const loc = useLocation();

  return (
    <nav aria-label="Primary" className="flex-1 overflow-y-auto pb-3">
      <GroupLabel collapsed={collapsed}>{WORKLOADS_GROUP.label}</GroupLabel>
      <div className="space-y-0.5 px-1.5">
        {WORKLOADS_GROUP.items.map((item) => (
          <NavItemLink key={item.to} item={item} collapsed={collapsed} onNavigate={onNavigate} />
        ))}
      </div>

      <GroupLabel collapsed={collapsed}>{NOTIFICATION_GROUP.label}</GroupLabel>
      <div className="space-y-0.5 px-1.5">
        {NOTIFICATION_GROUP.items.map((item) => (
          <NavItemLink key={item.to} item={item} collapsed={collapsed} onNavigate={onNavigate} />
        ))}
      </div>

      <GroupLabel collapsed={collapsed}>{ACCOUNT_GROUP.label}</GroupLabel>
      <div className="space-y-0.5 px-1.5">
        {ACCOUNT_GROUP.items.map((item) => (
          <NavItemLink key={item.to} item={item} collapsed={collapsed} onNavigate={onNavigate} />
        ))}
      </div>

      {isAdmin && (
        <>
          <GroupLabel collapsed={collapsed}>Admin</GroupLabel>
          <div className="space-y-0.5 px-1.5">
            {ADMIN_SUBGROUPS.map((g) => (
              <AdminAccordion
                key={g.label}
                group={g}
                collapsed={collapsed}
                pathname={loc.pathname}
                onNavigate={onNavigate}
              />
            ))}
          </div>
        </>
      )}
    </nav>
  );
}

// Desktop sidebar: a fixed-width column flexed alongside the main scroll
// area. Width animates between 240/64px. Toggle persists via zustand.
//
// At 768-1023px (`forceCollapsed`) the rail stays at 64px in the flex flow,
// but hovering or focusing into it pops a 240px expanded panel as an
// absolute overlay (z-40) so we don't push the page content. Click/tap on
// any nav link still works on the icon rail underneath; the overlay is a
// pure progressive enhancement and never traps pointer events when not
// hovered.
export function Sidebar({ forceCollapsed = false }: { forceCollapsed?: boolean }) {
  const collapsedPref = useLayoutStore((s) => s.sidebarCollapsed);
  const toggle = useLayoutStore((s) => s.toggleSidebar);
  const [hoverExpanded, setHoverExpanded] = useState(false);
  // Persistent collapsed reflects the user's preference; tablet adds an
  // implicit force-collapse on top of that. The visual collapsed flag is
  // the OR of the two — except the tablet hover/focus state can locally
  // override it.
  const baseCollapsed = forceCollapsed || collapsedPref;
  const collapsed = baseCollapsed && !(forceCollapsed && hoverExpanded);

  return (
    <aside
      onMouseEnter={() => forceCollapsed && setHoverExpanded(true)}
      onMouseLeave={() => forceCollapsed && setHoverExpanded(false)}
      onFocus={() => forceCollapsed && setHoverExpanded(true)}
      onBlur={(e) => {
        // Collapse only when focus leaves the entire aside, not when moving
        // between children. relatedTarget is null for pointer-driven blurs
        // — the mouseleave handler covers those.
        if (forceCollapsed && !e.currentTarget.contains(e.relatedTarget as Node | null)) {
          setHoverExpanded(false);
        }
      }}
      className={[
        "hidden md:flex h-full shrink-0 flex-col border-r border-border bg-panel/40 transition-[width] duration-200 ease-ui",
        // On tablet (forceCollapsed) we keep the underlying width fixed at
        // 16 so the layout doesn't shift; the expanded look is achieved by
        // absolutely overlaying the panel via the inner container.
        forceCollapsed ? "w-16 relative z-40" : collapsedPref ? "w-16" : "w-60",
        forceCollapsed && hoverExpanded ? "shadow-panel-strong" : "",
      ].join(" ")}
    >
      <div
        className={[
          "flex h-full flex-col",
          // When tablet-hover-expanded we absolutely position the inner
          // pane to a wider footprint without affecting the flex track.
          forceCollapsed && hoverExpanded
            ? "absolute inset-y-0 left-0 w-60 border-r border-border bg-panel"
            : "w-full",
        ].join(" ")}
      >
        <div className={["flex items-center border-b border-border", collapsed ? "justify-center px-0 py-2" : "justify-between px-3 py-2"].join(" ")}>
          {!collapsed && (
            <span className="text-[10px] font-semibold uppercase tracking-wider text-fg-subtle">
              Navigation
            </span>
          )}
          <button
            type="button"
            onClick={toggle}
            // Hide the toggle on tablet — the rail is forced collapsed
            // there and the hover overlay handles expansion. Showing the
            // toggle would let the user "expand" but the click would only
            // toggle the persisted preference, which has no effect when
            // forceCollapsed is true.
            aria-label={collapsed ? "Expand sidebar" : "Collapse sidebar"}
            aria-expanded={!collapsed}
            className={[
              "inline-flex h-7 w-7 items-center justify-center rounded-md border border-border bg-panel text-fg-muted transition-colors hover:bg-panel-2 hover:text-fg focus:outline-none focus-visible:ring-2 focus-visible:ring-accent/40",
              forceCollapsed ? "invisible" : "",
            ].join(" ")}
          >
            {collapsed ? <PanelLeftOpen className="h-3.5 w-3.5" /> : <PanelLeftClose className="h-3.5 w-3.5" />}
          </button>
        </div>
        <SidebarNav collapsed={collapsed} />
      </div>
    </aside>
  );
}
