import { create } from "zustand";
import { persist } from "zustand/middleware";

// Layout state for the AppShell. Currently only tracks the desktop sidebar
// collapsed flag — persisted to localStorage so the user's preference
// survives reload. The mobile drawer is purely transient (lives in
// component state) since it's tied to per-page navigation, not preference.
//
// Storage key is `monsys.sidebar.collapsed` (a thin wrapper layout key —
// distinct from `mon-auth` so a logout doesn't drop the chrome preference).

interface LayoutState {
  sidebarCollapsed: boolean;
  setSidebarCollapsed: (collapsed: boolean) => void;
  toggleSidebar: () => void;
}

export const useLayoutStore = create<LayoutState>()(
  persist(
    (set) => ({
      sidebarCollapsed: false,
      setSidebarCollapsed: (collapsed) => set({ sidebarCollapsed: collapsed }),
      toggleSidebar: () => set((s) => ({ sidebarCollapsed: !s.sidebarCollapsed })),
    }),
    { name: "monsys.sidebar.collapsed" },
  ),
);
