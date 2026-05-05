// Zustand store for the Cmd+K command palette.
//
// ---------------------------------------------------------------------------
// Integration contract for Phase A's TopBar
// ---------------------------------------------------------------------------
//
// The TopBar's placeholder "search" / Cmd+K button must call the `toggle`
// action of this store from its onClick handler. Wire it like this:
//
//     import { useCommandPalette } from "../../lib/palette-store";
//     ...
//     const togglePalette = useCommandPalette((s) => s.toggle);
//     ...
//     <button onClick={togglePalette} aria-label="Open command palette">
//
// The TopBar should NOT manage the modal's open/closed state itself — the
// store is the single source of truth so the global Cmd+K hotkey (which
// `<CommandPalette />` registers) and the TopBar button stay in sync.
//
// `<CommandPalette />` itself is mounted once near the root of the AppShell
// (see the TODO(integration) marker inside CommandPalette.tsx). It reads
// `open` from the store, owns the hotkey listener, and pushes selections
// into `recent` via `addRecent`.
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
