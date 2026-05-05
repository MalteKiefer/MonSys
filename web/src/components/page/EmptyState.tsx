import { LucideIcon } from "lucide-react";
import { ReactNode } from "react";

// Centered "nothing here yet" block, reused across listing pages so the empty
// experience feels consistent: muted icon, short title, optional hint, and an
// optional primary action (typically the same CTA shown in the page header).
export function EmptyState({
  icon: Icon,
  title,
  hint,
  primaryAction,
}: {
  icon?: LucideIcon;
  title: ReactNode;
  hint?: ReactNode;
  primaryAction?: ReactNode;
}) {
  return (
    <div className="flex flex-col items-center gap-3 px-6 py-12 text-center">
      {Icon && <Icon className="h-10 w-10 text-fg-subtle" aria-hidden />}
      <p className="text-sm text-fg-muted">{title}</p>
      {hint && (
        <p className="max-w-md text-xs text-fg-subtle">{hint}</p>
      )}
      {primaryAction && <div className="mt-3">{primaryAction}</div>}
    </div>
  );
}
