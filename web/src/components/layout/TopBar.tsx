import { Activity, LogOut, Menu, Search } from "lucide-react";
import { useQueryClient } from "@tanstack/react-query";
import { useSyncExternalStore } from "react";
import { Link } from "react-router-dom";

import { ThemeToggle } from "../ThemeToggle";
import { api } from "../../lib/api";
import { useAuth } from "../../lib/auth";
import { getConnectionStatus, subscribe as subscribeConnection } from "../../lib/connection";

// Compact connection-status pill rendered inline on the topbar. The full
// red banner is still shown on connection loss (via AppShell), but this
// pill gives us a permanent indicator for the "ok" path so users always
// have a source of truth without having to wait for a failure to appear.
function ConnectionPill() {
  const status = useSyncExternalStore(subscribeConnection, getConnectionStatus, getConnectionStatus);
  const ok = status === "ok";
  return (
    <span
      role="status"
      aria-live="polite"
      title={ok ? "Connected to mon-server" : "Connection lost — retrying"}
      className={[
        "hidden items-center gap-1.5 rounded-md px-2 py-1 text-[11px] font-medium ring-1 ring-inset md:inline-flex",
        ok
          ? "bg-ok/10 text-ok ring-ok/30"
          : "bg-fail/10 text-fail ring-fail/30",
      ].join(" ")}
    >
      <span
        aria-hidden
        className={["h-1.5 w-1.5 rounded-full", ok ? "bg-ok animate-pulse-soft" : "bg-fail"].join(" ")}
      />
      {ok ? "Online" : "Offline"}
    </span>
  );
}

// Cmd+K search trigger. Phase F replaces this with the actual command
// palette. Until then we render a styled, focusable button so the spot
// reserved for the palette already looks lived-in. We don't bind any
// shortcut here on purpose — the palette will own the global key.
function SearchTrigger() {
  return (
    <button
      type="button"
      disabled
      aria-label="Open command palette (coming soon)"
      title="Coming soon"
      className="hidden w-72 cursor-not-allowed items-center gap-2 rounded-md border border-border bg-panel/60 px-2.5 py-1 text-xs text-fg-subtle transition-colors hover:bg-panel-2 hover:text-fg-muted md:inline-flex lg:w-80"
    >
      <Search className="h-3.5 w-3.5" aria-hidden />
      <span className="flex-1 text-left">Search…</span>
      <kbd className="rounded border border-border bg-bg/60 px-1.5 py-0.5 font-mono text-[10px] text-fg-subtle">
        ⌘K
      </kbd>
    </button>
  );
}

// User identity + sign-out cluster. On <md the email collapses to an avatar
// circle so the bar stays single-row even at 320px.
function UserBlock() {
  const { user, clear } = useAuth();
  const qc = useQueryClient();

  function logout() {
    api<unknown>("/v1/auth/logout", { method: "POST" }).catch(() => {});
    qc.clear();
    clear();
  }

  if (!user) return null;

  // Initials are derived from the email's local part, falling back to "?"
  // for the (extremely unlikely) edge of an empty string after the @.
  const initials = (user.email.split("@")[0] || "?").slice(0, 2).toUpperCase();

  return (
    <div className="flex items-center gap-2 text-sm">
      {user.totp_active && (
        <span className="hidden rounded-md bg-ok/10 px-1.5 py-0.5 text-[11px] font-medium text-ok ring-1 ring-inset ring-ok/30 sm:inline">
          2FA
        </span>
      )}
      <span className="hidden text-fg-muted lg:inline">{user.email}</span>
      <span
        className="inline-flex h-7 w-7 items-center justify-center rounded-full bg-panel-2 text-[11px] font-semibold text-fg-muted ring-1 ring-inset ring-border lg:hidden"
        aria-label={user.email}
        title={user.email}
      >
        {initials}
      </span>
      <ThemeToggle />
      <button
        type="button"
        onClick={logout}
        aria-label="Sign out"
        className="inline-flex items-center gap-1 rounded-md border border-border bg-panel px-2 py-1 text-xs text-fg-muted transition-colors hover:bg-panel-2 hover:text-fg"
      >
        <LogOut className="h-3.5 w-3.5" />
        <span className="hidden sm:inline">Sign out</span>
      </button>
    </div>
  );
}

// TopBar renders the sticky header. The hamburger only appears on <md;
// AppShell uses the same store-binding to keep the icon and the drawer in
// sync.
export function TopBar({ onOpenMobile }: { onOpenMobile: () => void }) {
  return (
    <header className="sticky top-0 z-30 flex h-12 items-center justify-between border-b border-border bg-bg/85 px-3 backdrop-blur supports-[backdrop-filter]:bg-bg/70 md:px-5">
      <div className="flex min-w-0 items-center gap-3">
        <button
          type="button"
          onClick={onOpenMobile}
          aria-label="Open navigation"
          aria-haspopup="dialog"
          className="inline-flex h-8 w-8 items-center justify-center rounded-md border border-border bg-panel text-fg-muted transition-colors hover:bg-panel-2 hover:text-fg md:hidden"
        >
          <Menu className="h-4 w-4" />
        </button>
        <Link to="/" className="flex items-center gap-2 text-sm font-semibold tracking-tight">
          <Activity className="h-4 w-4 text-accent" aria-hidden />
          <span>MonSys</span>
        </Link>
      </div>
      <div className="hidden flex-1 items-center justify-center px-4 md:flex">
        <SearchTrigger />
      </div>
      <div className="flex items-center gap-2 md:gap-3">
        <ConnectionPill />
        <UserBlock />
      </div>
    </header>
  );
}
