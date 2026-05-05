import type { Host } from "./types";

// hostDisplay returns a user-facing label for a host. Operators often refer to
// hosts by a friendly `labels.host` value (e.g. "tail") rather than the raw
// hostname (e.g. "headscale"). When the two differ, render both — "tail
// (headscale)" — so a host is recognisable by either name. When the label is
// missing or matches the hostname, fall back to just the hostname to avoid
// duplication.
export function hostDisplay(h: Pick<Host, "hostname" | "labels">): string {
  const label = h.labels?.host?.trim();
  return label && label !== h.hostname ? `${label} (${h.hostname})` : h.hostname;
}
