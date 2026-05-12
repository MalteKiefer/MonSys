import { Activity, X } from "lucide-react";
import { useEffect, useRef } from "react";
import { useLocation } from "react-router-dom";

import { SidebarNav } from "./Sidebar";
import { useT } from "../../i18n/useT";

// Mobile-only navigation drawer. Renders nothing while closed (the parent
// just stops mounting it) so we don't leak focus traps or scroll-locks for
// users who never tap the hamburger.
//
// Behaviour:
//   - opens when AppShell sets `open` to true;
//   - closes on backdrop tap, Escape, or any route change;
//   - locks body scroll while open;
//   - focuses the close button on open as a minimal focus-trap entry — we
//     don't implement a full trap (no listing of all tabbables) because the
//     drawer only contains nav links + one close button, and Tab cycles
//     within the drawer naturally with `inert` siblings.
export function MobileDrawer({ open, onClose }: { open: boolean; onClose: () => void }) {
  const { t } = useT("nav");
  const closeRef = useRef<HTMLButtonElement | null>(null);
  const loc = useLocation();

  // Close on route navigation. We compare the pathname identity so that
  // an in-place state replacement (e.g. tab switching inside a page) does
  // NOT spuriously dismiss the drawer.
  useEffect(() => {
    if (open) onClose();
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [loc.pathname]);

  useEffect(() => {
    if (!open) return;
    function onKey(e: KeyboardEvent) {
      if (e.key === "Escape") {
        e.preventDefault();
        onClose();
      }
    }
    window.addEventListener("keydown", onKey);
    return () => window.removeEventListener("keydown", onKey);
  }, [open, onClose]);

  // Focus the close button when the dialog opens — gives keyboard users a
  // sane landing point and keeps Tab inside the drawer (the close button is
  // the first focusable; Shift+Tab from the first nav link wraps to it).
  useEffect(() => {
    if (open) closeRef.current?.focus();
  }, [open]);

  // Body scroll-lock. We restore the previous overflow value rather than
  // hard-clearing it so co-existing modals (EnrollAgentModal, etc.) don't
  // get their lock stomped if they happen to overlap.
  useEffect(() => {
    if (!open) return;
    const prev = document.body.style.overflow;
    document.body.style.overflow = "hidden";
    return () => {
      document.body.style.overflow = prev;
    };
  }, [open]);

  if (!open) return null;

  return (
    <div className="fixed inset-0 z-50 md:hidden" role="dialog" aria-modal="true" aria-label={t("actions.open_navigation")}>
      <button
        type="button"
        aria-label={t("actions.close_navigation")}
        onClick={onClose}
        className="absolute inset-0 cursor-default bg-black/50 backdrop-blur-sm"
      />
      <div className="absolute inset-y-0 left-0 flex w-72 max-w-[85vw] flex-col border-r border-border bg-bg shadow-panel-strong">
        <div className="flex h-12 items-center justify-between border-b border-border px-3">
          <span className="flex items-center gap-2 text-sm font-semibold tracking-tight">
            <Activity className="h-4 w-4 text-accent" aria-hidden />
            MonSys
          </span>
          <button
            ref={closeRef}
            type="button"
            onClick={onClose}
            aria-label={t("actions.close_navigation")}
            className="inline-flex h-8 w-8 items-center justify-center rounded-md border border-border bg-panel text-fg-muted transition-colors hover:bg-panel-2 hover:text-fg focus:outline-none focus-visible:ring-2 focus-visible:ring-accent/40"
          >
            <X className="h-4 w-4" />
          </button>
        </div>
        <SidebarNav collapsed={false} onNavigate={onClose} />
      </div>
    </div>
  );
}
