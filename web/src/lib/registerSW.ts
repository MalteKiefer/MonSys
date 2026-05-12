// Registers the auto-updating service worker built by vite-plugin-pwa.
//
// The plugin generates a `virtual:pwa-register` module at build time; in
// dev it's a no-op stub so the registration import is safe to keep
// unconditionally in main.tsx. Strategy is "autoUpdate" (configured in
// vite.config.ts) — when a new build is deployed, the SW silently swaps
// itself out on the next navigation. We don't surface a prompt today;
// the hooks below are wired so a future "new version available" toast
// can be slotted in without touching main.tsx.
import { registerSW } from "virtual:pwa-register";

export const updateSW = registerSW({
  onNeedRefresh() {
    // Future: surface a "new version available, click to refresh" UX.
    // Auto-update on next navigation is the current behaviour.
  },
  onOfflineReady() {
    // App shell is cached for offline use. No UI today — the install
    // affordance and the offline-ready ping are both implicit.
  },
});
