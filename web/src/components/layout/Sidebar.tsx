import {
  Activity,
  AlertTriangle,
  Bell,
  ChevronDown,
  Cog,
  FileSearch,
  LayoutDashboard,
  LogOut,
  MessageSquare,
  Network,
  Package,
  PanelLeftClose,
  PanelLeftOpen,
  Radio,
  Server,
  ShieldCheck,
  Stethoscope,
  User as UserIcon,
  Users,
} from "lucide-react";
import { useQueryClient } from "@tanstack/react-query";
import { useEffect, useMemo, useState } from "react";
import { NavLink, useLocation, useNavigate } from "react-router-dom";

import { Avatar, DropdownMenu } from "../ui";
import { api } from "../../lib/api";
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

export const NOTIFICATION_GROUP: NavGroup = {
  label: "Notifications",
  items: [
    { to: "/notifications", label: "Rules", icon: Bell, end: true },
    { to: "/notifications/channels", label: "Channels", icon: MessageSquare },
    { to: "/notifications/alerts", label: "Alerts", icon: AlertTriangle },
  ],
};

// Renamed from "Workloads" to "Overview" — the group holds the dashboard,
// hosts, packages, and monitors which aren't all workloads. The constant
// name stays WORKLOADS_GROUP for backwards compatibility with any future
// importers that pick it up from the module surface.
export const WORKLOADS_GROUP: NavGroup = {
  label: "Overview",
  items: [
    { to: "/", label: "Dashboard", icon: LayoutDashboard, end: true },
    { to: "/hosts", label: "Hosts", icon: Server },
    { to: "/packages", label: "Packages", icon: Package },
    { to: "/monitors", label: "Monitors", icon: Radio },
  ],
};

// Account group is intentionally empty — Profile and Sign out are reachable
// from the avatar dropdown at the top of the sidebar instead. Kept as a
// named export so consumers that imported it don't break, but the sidebar
// no longer renders it.
export const ACCOUNT_GROUP: NavGroup = {
  label: "Account",
  items: [],
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
      // Server logs, agent ingests, and audit log are consolidated under
      // /admin/logs as tabs — the page reads `?tab=` to pre-select one.
      { to: "/admin/logs", label: "Logs", icon: FileSearch },
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

// UserCard sits at the top of the sidebar and shows the signed-in user
// with an avatar + email + role, plus a chevron that opens a dropdown
// with Profile / Sign out. This replaces the old ACCOUNT_GROUP nav entry
// for Profile and the standalone logout button on the topbar — both
// affordances live here now so the user has a single recognizable target.
function UserCard({ collapsed, onNavigate }: { collapsed: boolean; onNavigate?: () => void }) {
  const user = useAuth((s) => s.user);
  const clear = useAuth((s) => s.clear);
  const navigate = useNavigate();
  const qc = useQueryClient();

  if (!user) return null;

  // Mirrors TopBar's prior logout flow: fire-and-forget the server-side
  // logout, then nuke local query cache and auth state and bounce to the
  // login screen. The catch swallows the network error on purpose — the
  // user is leaving the app either way; the server-side session will
  // expire on its own if the call fails.
  function logout() {
    api<unknown>("/v1/auth/logout", { method: "POST" }).catch(() => {});
    qc.clear();
    clear();
    onNavigate?.();
    navigate("/login", { replace: true });
  }

  const items = [
    {
      key: "profile",
      label: "Profile",
      icon: UserIcon,
      onClick: () => {
        onNavigate?.();
        navigate("/profile");
      },
    },
    {
      key: "logout",
      label: "Sign out",
      icon: LogOut,
      destructive: true,
      onClick: logout,
    },
  ];

  if (collapsed) {
    // Just an avatar button that opens the menu. In collapsed mode there's
    // no room for email/role text.
    return (
      <div className="flex justify-center px-2 py-2">
        <DropdownMenu
          align="left"
          trigger={
            <button
              type="button"
              aria-label={`Account menu for ${user.email}`}
              title={user.email}
              className="rounded-full focus:outline-none focus-visible:ring-2 focus-visible:ring-accent/40"
            >
              <Avatar
                userId={user.id}
                hasAvatar={user.has_avatar}
                updatedAt={user.avatar_updated_at}
                email={user.email}
                size="sm"
              />
            </button>
          }
          items={items}
        />
      </div>
    );
  }

  return (
    <div className="px-2 py-2">
      <DropdownMenu
        align="left"
        trigger={
          <button
            type="button"
            aria-label={`Account menu for ${user.email}`}
            className="flex w-full min-w-0 items-center gap-2 rounded-md border border-border bg-panel/60 px-2 py-1.5 text-left transition-colors hover:bg-panel-2 focus:outline-none focus-visible:ring-2 focus-visible:ring-accent/40"
          >
            <Avatar
              userId={user.id}
              hasAvatar={user.has_avatar}
              updatedAt={user.avatar_updated_at}
              email={user.email}
              size="sm"
            />
            <span className="flex min-w-0 flex-1 flex-col leading-tight">
              <span className="truncate text-xs font-medium text-fg">{user.email}</span>
              <span className="truncate text-[10px] uppercase tracking-wider text-fg-subtle">
                {user.role}
              </span>
            </span>
            <ChevronDown className="h-3.5 w-3.5 shrink-0 text-fg-subtle" aria-hidden />
          </button>
        }
        items={items}
      />
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
    <nav aria-label="Primary" className="flex min-h-0 flex-1 flex-col overflow-y-auto pb-3">
      <UserCard collapsed={collapsed} onNavigate={onNavigate} />

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
