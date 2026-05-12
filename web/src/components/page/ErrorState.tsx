import { AlertCircle } from "lucide-react";
import type { ReactNode } from "react";

import { Button } from "../ui";

// Inline error block for failed queries / mutations. Shows the message
// alongside an alert icon and an optional Retry button. Use this in place of
// bespoke `<p className="text-fail">…</p>` blocks so retry behaviour is
// uniform across pages.
export function ErrorState({
  message,
  onRetry,
  retryLabel = "Retry",
}: {
  message: ReactNode;
  onRetry?: () => void;
  retryLabel?: string;
}) {
  return (
    <div
      role="alert"
      className="flex flex-wrap items-start gap-3 rounded-md border border-fail/30 bg-fail/10 px-4 py-3 text-sm text-fail"
    >
      <AlertCircle className="mt-0.5 h-4 w-4 shrink-0" aria-hidden />
      <div className="min-w-0 flex-1 break-words">{message}</div>
      {onRetry && (
        <Button variant="secondary" onClick={onRetry}>
          {retryLabel}
        </Button>
      )}
    </div>
  );
}
