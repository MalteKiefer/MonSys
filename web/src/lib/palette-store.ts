// Zustand store for the Cmd+K command palette.
//
// ---------------------------------------------------------------------------
// Integration topology (single source of truth: this store)
// ---------------------------------------------------------------------------
//
// Three call sites consume this hook today; the store is the single source
// of truth so they all stay in sync:
//
//   1. `<CommandPalette />` — mounted once near the root of the AppShell
//      (see web/src/App.tsx, where it sits as a sibling of the router).
//      It reads `open` from this store, owns the global Cmd+K / Ctrl+K
//      hotkey listener (see useGlobalHotkey in CommandPalette.tsx), and
//      pushes the user's last selection into `recent` via `addRecent`.
//
//   2. `<TopBar />` — the header's search-shaped button calls the
//      `toggle` action from this store on click (see TopBar.tsx). It
//      explicitly does NOT manage open/closed state itself; the store
//      owns it so the hotkey and the button can't disagree.
//
//   3. Any future "open the palette pre-populated" entry points should
//      use the same pattern: `useCommandPalette((s) => s.setOpen)(true)`
//      rather than rendering their own modal.
//
// Recent items are persisted to localStorage under the key
// `monsys.palette.recent` so the user's most-used jumps survive reloads.
// We keep at most 5 entries.
// ---------------------------------------------------------------------------

import { create } from "zustand";
import { persist, createJSONStorage } from "zustand/middleware";

// A "recent" entry is a tiny denormalised pointer at whatever the user
// selected last. We do not persist transient metadata (e.g. last-known
// status) — the palette re-resolves the live label from its sources on next
// open. Only the bare minimum required to render and re-navigate is kept.
export type PaletteRecent = {
  // Stable identifier within `kind` — for "page" this is the route path, for
  // "host"/"monitor"/"rule" this is the entity ID. Combined with `kind` it
  // uniquely keys a row.
  id: string;
  kind: "page" | "host" | "monitor" | "rule";
  label: string;
  to: string;
};

const RECENT_LIMIT = 5;

type PaletteState = {
  // ---- transient (not persisted) -----------------------------------------
  open: boolean;
  setOpen: (open: boolean) => void;
  toggle: () => void;
  // ---- persisted ---------------------------------------------------------
  recent: PaletteRecent[];
  addRecent: (entry: PaletteRecent) => void;
  clearRecent: () => void;
};

export const useCommandPalette = create<PaletteState>()(
  persist(
    (set) => ({
      open: false,
      setOpen: (open) => set({ open }),
      toggle: () => set((s) => ({ open: !s.open })),

      recent: [],
      // Push to the front, dedupe by (kind,id), cap at RECENT_LIMIT. Most
      // recent first so the empty-input view can render them in order.
      addRecent: (entry) =>
        set((s) => {
          const filtered = s.recent.filter(
            (r) => !(r.kind === entry.kind && r.id === entry.id),
          );
          return { recent: [entry, ...filtered].slice(0, RECENT_LIMIT) };
        }),
      clearRecent: () => set({ recent: [] }),
    }),
    {
      name: "monsys.palette.recent",
      storage: createJSONStorage(() => localStorage),
      // Only persist `recent`. `open` is transient — we never want the
      // palette to spring open on a fresh page load just because the user
      // closed the tab while it was visible.
      partialize: (s) => ({ recent: s.recent }),
    },
  ),
);
