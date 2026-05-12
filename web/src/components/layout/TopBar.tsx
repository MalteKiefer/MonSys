import { Activity, Check, Globe, Menu, Search } from "lucide-react";
import { useSyncExternalStore } from "react";
import { Link } from "react-router-dom";
import { useTranslation } from "react-i18next";
import { useQueryClient } from "@tanstack/react-query";

import { Avatar, DropdownMenu, type DropdownItem } from "../ui";
import { ThemeToggle } from "../ThemeToggle";
import { useCommandPalette } from "../../lib/palette-store";
import { useAuth } from "../../lib/auth";
import { getConnectionStatus, subscribe as subscribeConnection } from "../../lib/connection";
import { setLocale, SUPPORTED_LOCALES, type SupportedLocale } from "../../i18n";
import { api } from "../../lib/api";

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

// Cmd+K search trigger. The CommandPalette owns the global Cmd+K / Ctrl+K
// listener; this button is just an alternate entry point for pointer users.
function SearchTrigger() {
  const toggle = useCommandPalette((s) => s.toggle);
  return (
    <button
      type="button"
      onClick={toggle}
      aria-label="Open command palette (Cmd+K)"
      className="hidden w-72 items-center gap-2 rounded-md border border-border bg-panel/60 px-2.5 py-1 text-xs text-fg-subtle transition-colors hover:bg-panel-2 hover:text-fg-muted md:inline-flex lg:w-80"
    >
      <Search className="h-3.5 w-3.5" aria-hidden />
      <span className="flex-1 text-left">Search…</span>
      <kbd className="rounded border border-border bg-bg/60 px-1.5 py-0.5 font-mono text-[10px] text-fg-subtle">
        ⌘K
      </kbd>
    </button>
  );
}

// LanguageSwitcher — compact button with the active locale code in caps.
// Click opens a DropdownMenu with "Auto" (clears the persisted choice and
// falls back to browser detection) and explicit "English" / "Deutsch"
// entries. Locale change is optimistic locally; the server PUT runs in the
// background and only invalidates the `me` query so the auth store picks up
// the new value once it lands.
function LanguageSwitcher() {
  const { i18n } = useTranslation();
  const user = useAuth((s) => s.user);
  const qc = useQueryClient();

  // The runtime locale (i18next.language) is the source of truth for what
  // the UI is currently rendering; the persisted preference may legitimately
  // disagree until the next reload, but the badge should reflect what the
  // user sees right now.
  const raw = (i18n.language || "en").toLowerCase();
  const base = raw.split("-")[0];
  const current: SupportedLocale = (SUPPORTED_LOCALES as readonly string[]).includes(base)
    ? (base as SupportedLocale)
    : "en";

  // Whether the persisted preference is "auto" (i.e. no explicit choice
  // stored). When set, the Check moves to "Auto" instead of the resolved
  // language so the menu reflects intent, not just detection output.
  const isAuto = !user?.language;

  function persist(value: "auto" | SupportedLocale) {
    if (!useAuth.getState().token) return;
    // Fire-and-forget; the api() helper already reports liveness and surfaces
    // 401s into the auth store. We don't await the PUT — the user shouldn't
    // wait for the round-trip to see the UI flip. On success the invalidation
    // refetches /v1/auth/me which carries the new `language` into the auth
    // store via App.tsx's effect.
    void api<{ ok: boolean }>("/v1/auth/me/language", {
      method: "POST",
      body: JSON.stringify({ language: value === "auto" ? "" : value }),
    })
      .then(() => qc.invalidateQueries({ queryKey: ["me"] }))
      .catch(() => {
        /* swallow — local change still applied; next /me refresh corrects */
      });
  }

  function choose(value: "auto" | SupportedLocale) {
    if (value === "auto") {
      // Drop the localStorage override; i18next-browser-languagedetector
      // will fall back to navigator.language on next init. For *this* paint
      // we resolve navigator.language down to a supported locale so the UI
      // updates immediately without a reload.
      try {
        localStorage.removeItem("mon-lang");
      } catch {
        /* private browsing — no-op */
      }
      const nav = (typeof navigator !== "undefined" ? navigator.language : "en")
        .toLowerCase()
        .split("-")[0];
      const resolved: SupportedLocale = (SUPPORTED_LOCALES as readonly string[]).includes(nav)
        ? (nav as SupportedLocale)
        : "en";
      void i18n.changeLanguage(resolved);
    } else {
      setLocale(value);
    }
    persist(value);
  }

  // DropdownMenu only renders a single leading icon per item. We use that
  // slot as a binary "is this the active row?" indicator: Check for active,
  // Globe for the inactive entries (so every row has *some* glyph and the
  // label column lines up).
  const items: DropdownItem[] = [
    {
      key: "auto",
      label: "Auto",
      icon: isAuto ? Check : Globe,
      onClick: () => choose("auto"),
    },
    {
      key: "en",
      label: "English",
      icon: !isAuto && current === "en" ? Check : Globe,
      onClick: () => choose("en"),
    },
    {
      key: "de",
      label: "Deutsch",
      icon: !isAuto && current === "de" ? Check : Globe,
      onClick: () => choose("de"),
    },
  ];

  return (
    <DropdownMenu
      align="right"
      trigger={
        <button
          type="button"
          aria-label="Change language"
          className="inline-flex h-8 min-w-8 items-center justify-center rounded-md border border-border bg-panel px-2 text-[11px] font-semibold uppercase tracking-wide text-fg-muted transition-colors hover:bg-panel-2 hover:text-fg focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-accent/40"
        >
          {current}
        </button>
      }
      items={items}
    />
  );
}

// User identity cluster. The full account menu (Profile / Sign out) lives
// in the sidebar's UserCard now — the topbar keeps a compact avatar + 2FA
// pill + theme toggle so the bar still says "you are signed in" at a
// glance without duplicating the menu trigger.
function UserBlock() {
  const user = useAuth((s) => s.user);
  if (!user) return null;

  return (
    <div className="flex items-center gap-2 text-sm">
      {user.totp_active && (
        <span className="hidden rounded-md bg-ok/10 px-1.5 py-0.5 text-[11px] font-medium text-ok ring-1 ring-inset ring-ok/30 sm:inline">
          2FA
        </span>
      )}
      <Avatar
        userId={user.id}
        hasAvatar={user.has_avatar}
        updatedAt={user.avatar_updated_at}
        email={user.email}
        size="sm"
      />
      <LanguageSwitcher />
      <ThemeToggle />
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
