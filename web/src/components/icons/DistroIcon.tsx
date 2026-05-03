// Tiny SVG glyphs per distro family. We avoid pulling in an icon-font
// package — these are simple symbolic marks, not exact brand logos.

import type { ReactElement } from "react";
import { Server } from "lucide-react";

const PALETTE: Record<string, string> = {
  arch: "#1793D1",
  ubuntu: "#E95420",
  debian: "#A80030",
  fedora: "#294172",
  rhel: "#EE0000",
  alpine: "#0D597F",
  suse: "#73BA25",
  nixos: "#5277C3",
};

const PATHS: Record<string, ReactElement> = {
  // Stylized "A" silhouette for Arch.
  arch: <path d="M12 2 L20 20 H4 Z M12 9 L8 18 H16 Z" fill="currentColor" />,
  // Ubuntu-ish circle of friends (3 dots + ring).
  ubuntu: (
    <>
      <circle cx="12" cy="12" r="9" fill="none" stroke="currentColor" strokeWidth="1.5" />
      <circle cx="12" cy="4.5" r="1.6" fill="currentColor" />
      <circle cx="5.5" cy="15.5" r="1.6" fill="currentColor" />
      <circle cx="18.5" cy="15.5" r="1.6" fill="currentColor" />
    </>
  ),
  // Debian swirl simplified.
  debian: (
    <path
      d="M16 12a4 4 0 1 1-7.4-2.1 5 5 0 1 0 6.6 6.6A4 4 0 0 1 16 12z"
      fill="none"
      stroke="currentColor"
      strokeWidth="1.5"
    />
  ),
  // Fedora "f" infinity symbol.
  fedora: (
    <path
      d="M12 4a8 8 0 1 0 0 16 4 4 0 1 0 0-8h-2"
      fill="none"
      stroke="currentColor"
      strokeWidth="1.6"
      strokeLinecap="round"
    />
  ),
  // Red hat silhouette.
  rhel: (
    <path
      d="M3 16c2-3 6-5 9-5s7 2 9 5l-1 3H4z"
      fill="currentColor"
    />
  ),
  // Alpine mountain.
  alpine: (
    <path
      d="M3 19l5-9 4 6 3-4 6 7z"
      fill="currentColor"
    />
  ),
  // SUSE gecko-shaped chevron.
  suse: (
    <path
      d="M5 19c0-6 5-12 14-12-2 6-7 12-14 12zM10 9l3-1"
      fill="none"
      stroke="currentColor"
      strokeWidth="1.6"
      strokeLinecap="round"
    />
  ),
  // NixOS hex pattern.
  nixos: (
    <path
      d="M12 3l9 5.2v7.6L12 21l-9-5.2V8.2zM12 8l5 3v2l-5 3-5-3v-2z"
      fill="none"
      stroke="currentColor"
      strokeWidth="1.4"
      strokeLinejoin="round"
    />
  ),
};

export function DistroIcon({ family, size = 16 }: { family?: string; size?: number }) {
  if (!family || !PATHS[family]) {
    return <Server size={size} className="text-fg-subtle" aria-label="unknown distro" />;
  }
  const color = PALETTE[family] ?? "currentColor";
  return (
    <svg
      width={size}
      height={size}
      viewBox="0 0 24 24"
      role="img"
      aria-label={family}
      style={{ color, flexShrink: 0 }}
    >
      {PATHS[family]}
    </svg>
  );
}
