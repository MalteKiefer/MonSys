// TODO(integration): Phase A owns App.tsx — once that lands, mount
// `<DensityProvider />` once at the root (e.g. directly inside <App>) so the
// `data-density` attribute on <html> is in sync from the first paint. Until
// then the Profile page mounts it via its `useDensity()` consumer; the
// effect is idempotent so double-mounting is safe.
//
// Density toggle store. Two modes:
//   - "comfortable" (default) — current layout, larger paddings.
//   - "compact"               — tighter table cells, panels, smaller font.
//
// The actual visual rules live in src/index.css under
// `html[data-density="compact"] …`. This file is purely state + the side
// effect that mirrors the current value to <html data-density="…">.

import { useEffect } from "react";
import { create } from "zustand";
import { persist } from "zustand/middleware";

export type Density = "compact" | "comfortable";

type DensityState = {
  density: Density;
  setDensity: (d: Density) => void;
  toggle: () => void;
};

export const useDensityStore = create<DensityState>()(
  persist(
    (set) => ({
      density: "comfortable",
      setDensity: (density) => set({ density }),
      toggle: () =>
        set((s) => ({ density: s.density === "compact" ? "comfortable" : "compact" })),
    }),
    { name: "monsys.density" },
  ),
);

/**
 * Apply the persisted density attribute to <html> on mount and whenever it
 * changes. Render this once at the root (today: from Profile via the
 * consumer; tomorrow: from App.tsx) — no children needed; it is purely a
 * side effect bridge.
 */
export function DensityProvider() {
  const density = useDensityStore((s) => s.density);
  useEffect(() => {
    if (typeof document === "undefined") return;
    document.documentElement.setAttribute("data-density", density);
  }, [density]);
  return null;
}
