// Cmd+K / Ctrl+K command palette modal.
//
// Sources of indexable items:
//   1. Static pages — hardcoded mirror of the routes mounted by App.tsx
//   2. Hosts       — /v1/hosts (cached via react-query, fetched on first open)
//   3. Monitors    — /v1/monitors (admin only — same query-key as AdminMonitors)
//   4. Rules       — /v1/notifications/rules (admin only)
//   5. Recent      — last 5 selections, persisted via useCommandPalette()
//
// Filtering: case-insensitive substring across `label + secondary`. No fuzzy
// library — substring is enough and keeps the bundle small (no new deps).

import { useQuery } from "@tanstack/react-query";
import {
  Activity,
  Bell,
  ClipboardList,
  CornerDownLeft,
  FileJson,
  FileText,
  History,
  LayoutDashboard,
  Mail,
  Moon,
  Network,
  Package,
  Radio,
  Search,
  Server,
  ShieldCheck,
  Sliders,
  Ticket,
  UserCog,
  Users,
} from "lucide-react";
import type {
  KeyboardEvent as ReactKeyboardEvent} from "react";
import {
  useEffect,
  useMemo,
  useRef,
  useState,
} from "react";
import { useNavigate } from "react-router-dom";

import { api } from "../lib/api";
import { useAuth } from "../lib/auth";
import { useCommandPalette, type PaletteRecent } from "../lib/palette-store";
import { useT } from "../i18n/useT";
import type {
  Host,
  Monitor,
  NotificationRule,
} from "../lib/types";
import { hostDisplay } from "../lib/utils";

// ---------------------------------------------------------------------------
// Types
// ---------------------------------------------------------------------------

type ItemKind = "page" | "host" | "monitor" | "rule";

interface PaletteItem {
  // Stable key combining kind + id so React's reconciler stays happy across
  // re-renders and so addRecent can dedupe consistently.
  id: string;
  kind: ItemKind;
  label: string;
  // Secondary text rendered dim alongside the label and also indexed for
  // substring matching (lets users find "/admin/users" by typing "users"
  // even though "Users" already matches).
  secondary?: string;
  to: string;
  icon: typeof Search;
  // Optional category override for grouping. Defaults to a per-kind label.
  group?: string;
}

// Cap the result list at a sensible number so a 5,000-host fleet doesn't
// blow up the modal. 30 is enough that scrolling is never required for a
// typical query.
const MAX_RESULTS = 30;

// Group label per kind — used in the result heading and to keep ordering
// deterministic (Pages first, then Hosts, Monitors, Rules).
const GROUP_ORDER: Record<ItemKind, number> = {
  page: 0,
  host: 1,
  monitor: 2,
  rule: 3,
};
const GROUP_LABEL: Record<ItemKind, string> = {
  page: "Pages",
  host: "Hosts",
  monitor: "Monitors",
  rule: "Rules",
};

// ---------------------------------------------------------------------------
// Static page index
// ---------------------------------------------------------------------------
//
// Mirror of the routes mounted in App.tsx. Kept in this file rather than
// imported from the router so adding a new route doesn't accidentally
// surface in the palette before its label is curated. Admin-only routes
// are filtered out at render time when the user isn't an admin.

interface StaticPage {
  to: string;
  label: string;
  secondary: string;
  icon: typeof Search;
  adminOnly?: boolean;
}

const STATIC_PAGES: StaticPage[] = [
  { to: "/", label: "Overview", secondary: "/", icon: LayoutDashboard },
  { to: "/hosts", label: "Hosts", secondary: "/hosts", icon: Server },
  { to: "/packages", label: "Packages", secondary: "/packages", icon: Package },
  { to: "/monitors", label: "Monitors", secondary: "/monitors", icon: Radio },
  {
    to: "/notifications",
    label: "Notifications · Rules",
    secondary: "/notifications",
    icon: Bell,
  },
  { to: "/profile", label: "Profile", secondary: "/profile", icon: UserCog },

  // Admin
  { to: "/admin/groups", label: "Admin · Groups", secondary: "/admin/groups", icon: Network, adminOnly: true },
  { to: "/admin/users", label: "Admin · Users", secondary: "/admin/users", icon: Users, adminOnly: true },
  { to: "/admin/mail", label: "Admin · Mail (SMTP)", secondary: "/admin/mail", icon: Mail, adminOnly: true },
  { to: "/admin/quiet-hours", label: "Admin · Quiet hours", secondary: "/admin/quiet-hours", icon: Moon, adminOnly: true },
  { to: "/admin/agent-config", label: "Admin · Agent config", secondary: "/admin/agent-config", icon: Sliders, adminOnly: true },
  { to: "/admin/enrollments", label: "Admin · Enrollments", secondary: "/admin/enrollments", icon: Ticket, adminOnly: true },
  { to: "/admin/logs", label: "Admin · Server logs", secondary: "/admin/logs", icon: FileText, adminOnly: true },
  { to: "/admin/ingests", label: "Admin · Agent ingests", secondary: "/admin/ingests", icon: FileJson, adminOnly: true },
  { to: "/admin/security", label: "Admin · Security", secondary: "/admin/security", icon: ShieldCheck, adminOnly: true },
  { to: "/admin/audit", label: "Admin · Audit log", secondary: "/admin/audit", icon: ClipboardList, adminOnly: true },
];

// ---------------------------------------------------------------------------
// Hotkey listener (mounted by the modal even when closed)
// ---------------------------------------------------------------------------

function useGlobalHotkey() {
  const toggle = useCommandPalette((s) => s.toggle);
  const open = useCommandPalette((s) => s.open);
  const setOpen = useCommandPalette((s) => s.setOpen);

  useEffect(() => {
    function onKey(e: KeyboardEvent) {
      // Cmd+K on macOS, Ctrl+K everywhere else. We use the OR of
      // metaKey/ctrlKey rather than UA-sniffing — Linux users on Apple
      // keyboards then get both, which is harmless.
      if ((e.metaKey || e.ctrlKey) && e.key.toLowerCase() === "k") {
        // Prevent the browser's "focus address bar" / Firefox quick-find
        // shortcut. preventDefault is essential — without it Firefox eats
        // the keystroke and our toggle never fires while the modal is open.
        e.preventDefault();
        toggle();
        return;
      }
      // Escape always closes (the dialog handler also does this, but
      // catching it at the window level lets users dismiss even if focus
      // somehow escaped the trap).
      if (open && e.key === "Escape") {
        setOpen(false);
      }
    }
    window.addEventListener("keydown", onKey);
    return () => { window.removeEventListener("keydown", onKey); };
  }, [toggle, open, setOpen]);
}

// ---------------------------------------------------------------------------
// Component
// ---------------------------------------------------------------------------

export function CommandPalette() {
  useGlobalHotkey();
  const open = useCommandPalette((s) => s.open);
  if (!open) return null;
  return <PaletteModal />;
}

function PaletteModal() {
  const { t } = useT("nav");
  const setOpen = useCommandPalette((s) => s.setOpen);
  const recent = useCommandPalette((s) => s.recent);
  const addRecent = useCommandPalette((s) => s.addRecent);
  const navigate = useNavigate();
  const isAdmin = useAuth((s) => s.user?.role === "admin");

  const [query, setQuery] = useState("");
  const [activeIdx, setActiveIdx] = useState(0);
  const inputRef = useRef<HTMLInputElement>(null);
  const listRef = useRef<HTMLDivElement>(null);

  // Lock body scroll while open so the page underneath doesn't drift.
  useEffect(() => {
    const prev = document.body.style.overflow;
    document.body.style.overflow = "hidden";
    return () => {
      document.body.style.overflow = prev;
    };
  }, []);

  // Autofocus on open. The input is mounted at the same time as the modal
  // so a microtask delay isn't strictly required, but using requestAnimationFrame
  // keeps focus reliable across StrictMode double-invocation in dev.
  useEffect(() => {
    const id = requestAnimationFrame(() => inputRef.current?.focus());
    return () => { cancelAnimationFrame(id); };
  }, []);

  // ---- data sources -----------------------------------------------------
  //
  // Hosts query is gated on `enabled: open` so it only fires the first time
  // the user actually opens the palette. Once cached, react-query's default
  // staleTime covers subsequent opens. Monitors + rules are admin-only.

  const hostsQ = useQuery({
    queryKey: ["palette", "hosts"],
    queryFn: () => api<{ hosts: Host[] }>("/v1/hosts"),
    staleTime: 60_000,
  });
  const monitorsQ = useQuery({
    queryKey: ["palette", "monitors"],
    queryFn: () => api<{ monitors: Monitor[] }>("/v1/monitors"),
    enabled: isAdmin,
    staleTime: 60_000,
  });
  const rulesQ = useQuery({
    queryKey: ["palette", "rules"],
    queryFn: () => api<{ rules: NotificationRule[] }>("/v1/notifications/rules"),
    enabled: isAdmin,
    staleTime: 60_000,
  });

  // ---- index assembly ---------------------------------------------------

  const allItems = useMemo<PaletteItem[]>(() => {
    const items: PaletteItem[] = [];

    for (const p of STATIC_PAGES) {
      if (p.adminOnly && !isAdmin) continue;
      items.push({
        id: p.to,
        kind: "page",
        label: p.label,
        secondary: p.secondary,
        to: p.to,
        icon: p.icon,
      });
    }

    for (const h of hostsQ.data?.hosts ?? []) {
      const display = hostDisplay(h);
      items.push({
        id: h.id,
        kind: "host",
        label: `Open host: ${display}`,
        secondary: h.distro || h.hostname,
        to: `/hosts/${h.id}`,
        icon: Server,
      });
    }

    if (isAdmin) {
      for (const m of monitorsQ.data?.monitors ?? []) {
        items.push({
          id: m.id,
          kind: "monitor",
          label: `Monitor: ${m.name}`,
          secondary: `${m.type} · ${m.target}`,
          to: `/monitors`,
          icon: Radio,
        });
      }
      for (const r of rulesQ.data?.rules ?? []) {
        items.push({
          id: r.id,
          kind: "rule",
          // The spec suggests a `#rule-<id>` anchor, but that page doesn't
          // currently honour it. We deep-link to /notifications and let the
          // list view handle highlight in a follow-up.
          label: `Edit rule: ${r.name}`,
          secondary: `${r.condition_type} · ${r.severity}`,
          to: `/notifications#rule-${r.id}`,
          icon: Bell,
        });
      }
    }

    return items;
  }, [hostsQ.data, monitorsQ.data, rulesQ.data, isAdmin]);

  // Hydrate recents into PaletteItem shape (using item icons for the kind).
  // We keep them as a separate top group when the query is empty.
  const recentItems = useMemo<PaletteItem[]>(() => {
    return recent
      .map((r) => recentToItem(r))
      .filter((i): i is PaletteItem => i !== null);
  }, [recent]);

  // ---- filtering --------------------------------------------------------

  const trimmed = query.trim().toLowerCase();
  interface Section { kind: ItemKind | "recent"; label: string; items: PaletteItem[] }

  const sections = useMemo<Section[]>(() => {
    if (trimmed === "") {
      // Empty input: show Recent (top) then all Pages. This keeps the
      // first-open experience snappy — no waiting on /v1/hosts for the
      // initial render.
      const out: Section[] = [];
      if (recentItems.length > 0) {
        out.push({ kind: "recent", label: "Recent", items: recentItems });
      }
      const pageItems = allItems.filter((i) => i.kind === "page");
      if (pageItems.length > 0) {
        out.push({ kind: "page", label: GROUP_LABEL.page, items: pageItems });
      }
      return out;
    }

    // Non-empty: substring match across label + secondary, capped, grouped.
    const matched: PaletteItem[] = [];
    for (const item of allItems) {
      if (matched.length >= MAX_RESULTS) break;
      const hay = `${item.label} ${item.secondary ?? ""}`.toLowerCase();
      if (hay.includes(trimmed)) {
        matched.push(item);
      }
    }
    // Bucket by kind, preserving GROUP_ORDER for stable sectioning.
    const byKind = new Map<ItemKind, PaletteItem[]>();
    for (const item of matched) {
      const arr = byKind.get(item.kind) ?? [];
      arr.push(item);
      byKind.set(item.kind, arr);
    }
    return Array.from(byKind.entries())
      .sort((a, b) => GROUP_ORDER[a[0]] - GROUP_ORDER[b[0]])
      .map(([kind, items]) => ({ kind, label: GROUP_LABEL[kind], items }));
  }, [trimmed, allItems, recentItems]);

  // Flatten into the same order rendered, so keyboard navigation indexes
  // line up with what the user sees.
  const flat = useMemo<PaletteItem[]>(() => {
    const out: PaletteItem[] = [];
    for (const sec of sections) out.push(...sec.items);
    return out;
  }, [sections]);

  // Reset the active row to the top whenever the result set changes — keeps
  // the user from "scrolling past" rows that disappeared as they typed.
  useEffect(() => {
    setActiveIdx(0);
  }, [trimmed, flat.length]);

  // Scroll the active row into view as it changes (keyboard nav). Using
  // scrollIntoView with `nearest` avoids jumpy behaviour when the row is
  // already visible.
  useEffect(() => {
    const list = listRef.current;
    if (!list) return;
    const el = list.querySelector<HTMLElement>(`[data-idx="${activeIdx}"]`);
    el?.scrollIntoView({ block: "nearest" });
  }, [activeIdx]);

  // ---- selection -------------------------------------------------------

  function activate(item: PaletteItem) {
    addRecent({
      id: item.id,
      kind: item.kind,
      label: item.label,
      to: item.to,
    });
    setOpen(false);
    void navigate(item.to);
  }

  function onKeyDown(e: ReactKeyboardEvent<HTMLDivElement>) {
    if (e.key === "ArrowDown") {
      e.preventDefault();
      if (flat.length === 0) return;
      setActiveIdx((i) => (i + 1) % flat.length);
    } else if (e.key === "ArrowUp") {
      e.preventDefault();
      if (flat.length === 0) return;
      setActiveIdx((i) => (i - 1 + flat.length) % flat.length);
    } else if (e.key === "Home") {
      e.preventDefault();
      setActiveIdx(0);
    } else if (e.key === "End") {
      e.preventDefault();
      if (flat.length > 0) setActiveIdx(flat.length - 1);
    } else if (e.key === "Enter") {
      e.preventDefault();
      const item = flat[activeIdx];
      if (item) activate(item);
    } else if (e.key === "Escape") {
      e.preventDefault();
      setOpen(false);
    }
  }

  // Map flat-index -> section so we can render group headings inline while
  // still keeping a single linear keyboard cursor.
  let runningIdx = 0;
  const listboxId = "palette-listbox";
  const activeOptionId = flat[activeIdx]
    ? `palette-option-${activeIdx}`
    : undefined;

  return (
    <div
      role="presentation"
      // Backdrop covers the full viewport. Click-outside closes; we anchor
      // the panel by padding-top rather than flex centering so it sits at
      // ~18vh from the top regardless of the panel's own height.
      className="fixed inset-0 z-50 flex justify-center bg-bg/80 px-4 backdrop-blur-sm"
      style={{ paddingTop: "18vh" }}
      onMouseDown={(e) => {
        if (e.target === e.currentTarget) setOpen(false);
      }}
    >
      <div
        role="dialog"
        aria-modal="true"
        aria-label={t("palette.dialog_label")}
        className="flex w-full max-w-xl flex-col overflow-hidden rounded-lg border border-border bg-panel shadow-panel-strong"
        style={{ maxHeight: "60vh" }}
        onKeyDown={onKeyDown}
      >
        <div className="flex items-center gap-2 border-b border-border px-3 py-2.5">
          <Search className="h-4 w-4 shrink-0 text-fg-muted" aria-hidden />
          <input
            ref={inputRef}
            type="text"
            role="combobox"
            aria-expanded
            aria-controls={listboxId}
            aria-activedescendant={activeOptionId}
            aria-autocomplete="list"
            placeholder={t("palette.placeholder")}
            value={query}
            onChange={(e) => { setQuery(e.target.value); }}
            className="flex-1 bg-transparent text-sm text-fg placeholder:text-fg-subtle focus:outline-none"
          />
          <kbd className="hidden items-center rounded border border-border bg-panel-2 px-1.5 py-0.5 font-mono text-[10px] text-fg-subtle sm:inline-flex">
            Esc
          </kbd>
        </div>

        <div
          ref={listRef}
          id={listboxId}
          role="listbox"
          aria-label={t("palette.results_label")}
          className="flex-1 overflow-y-auto py-1"
        >
          {flat.length === 0 ? (
            <div className="px-4 py-8 text-center text-sm text-fg-muted">
              {trimmed === "" ? t("palette.empty_prompt") : t("palette.empty_results")}
            </div>
          ) : (
            sections.map((sec) => {
              const sectionStart = runningIdx;
              const node = (
                <div key={`${sec.kind}-${sec.label}`} className="py-1">
                  <div className="flex items-center gap-1.5 px-3 pb-1 pt-1 text-[10px] font-semibold uppercase tracking-wider text-fg-subtle">
                    {sec.kind === "recent" && (
                      <History className="h-3 w-3" aria-hidden />
                    )}
                    {sec.label}
                  </div>
                  <div className="space-y-0.5 px-1">
                    {sec.items.map((item) => {
                      const idx = sectionStart + sec.items.indexOf(item);
                      const isActive = idx === activeIdx;
                      const Icon = item.icon;
                      return (
                        <div
                          key={`${item.kind}-${item.id}`}
                          id={`palette-option-${idx}`}
                          data-idx={idx}
                          role="option"
                          aria-selected={isActive}
                          tabIndex={-1}
                          onMouseDown={(e) => {
                            // onMouseDown rather than onClick so the modal
                            // click-outside handler (also on mousedown)
                            // doesn't race and close before we navigate.
                            e.preventDefault();
                            activate(item);
                          }}
                          onMouseEnter={() => { setActiveIdx(idx); }}
                          className={[
                            "flex cursor-pointer items-center gap-2.5 rounded-md px-2.5 py-1.5 text-sm transition-colors",
                            isActive
                              ? "bg-panel-2 text-fg"
                              : "text-fg-muted hover:bg-panel-2/60 hover:text-fg",
                          ].join(" ")}
                        >
                          <Icon className="h-3.5 w-3.5 shrink-0" aria-hidden />
                          <span className="truncate">{item.label}</span>
                          {item.secondary && (
                            <span className="ml-auto truncate text-xs text-fg-subtle">
                              {item.secondary}
                            </span>
                          )}
                          {isActive && (
                            <CornerDownLeft
                              className="ml-1 h-3 w-3 shrink-0 text-fg-subtle"
                              aria-hidden
                            />
                          )}
                        </div>
                      );
                    })}
                  </div>
                </div>
              );
              runningIdx += sec.items.length;
              return node;
            })
          )}
        </div>

        <div className="flex items-center justify-between gap-3 border-t border-border bg-panel/40 px-3 py-1.5 text-[10px] text-fg-subtle">
          <div className="flex items-center gap-1">
            <Activity className="h-3 w-3" aria-hidden />
            <span>{t("palette.brand")}</span>
          </div>
          <div className="flex items-center gap-3">
            <span className="inline-flex items-center gap-1">
              <kbd className="rounded border border-border bg-panel-2 px-1 py-0.5 font-mono">↑↓</kbd>
              {t("palette.hint_navigate")}
            </span>
            <span className="inline-flex items-center gap-1">
              <kbd className="rounded border border-border bg-panel-2 px-1 py-0.5 font-mono">↵</kbd>
              {t("palette.hint_open")}
            </span>
          </div>
        </div>
      </div>
    </div>
  );
}

// ---------------------------------------------------------------------------
// Helpers
// ---------------------------------------------------------------------------

// Recent entries are persisted as bare pointers — when re-hydrating into a
// renderable PaletteItem we pick the icon from the kind. Returning null
// drops malformed entries (e.g. a future `kind` value that this build
// doesn't know about) rather than crashing.
function recentToItem(r: PaletteRecent): PaletteItem | null {
  let icon: typeof Search;
  switch (r.kind) {
    case "page":
      icon = LayoutDashboard;
      break;
    case "host":
      icon = Server;
      break;
    case "monitor":
      icon = Radio;
      break;
    case "rule":
      icon = Bell;
      break;
    default:
      return null;
  }
  return {
    id: r.id,
    kind: r.kind,
    label: r.label,
    to: r.to,
    icon,
  };
}

// Re-export so consumers (TopBar, AppShell) only need a single import path
// for both the modal and the store hook. Mirrors the pattern used by
// ./layout/Sidebar.tsx exporting both Sidebar and SidebarNav.
export { useCommandPalette } from "../lib/palette-store";
