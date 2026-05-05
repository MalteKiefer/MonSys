import { ReactNode } from "react";

import { Breadcrumb, BreadcrumbItem } from "./Breadcrumb";

// The visual top of every page: optional breadcrumb, then a row containing
// the H1 (with optional subtitle) and right-aligned actions. Stickiness is
// the AppShell's job — this component is intentionally non-sticky so it can
// be composed wherever.
export function PageHeader({
  title,
  subtitle,
  breadcrumb,
  actions,
}: {
  title: ReactNode;
  subtitle?: ReactNode;
  breadcrumb?: BreadcrumbItem[];
  actions?: ReactNode;
}) {
  return (
    <header className="space-y-2">
      {breadcrumb && breadcrumb.length > 0 && <Breadcrumb items={breadcrumb} />}
      <div className="flex flex-wrap items-start justify-between gap-3">
        <div className="min-w-0">
          <h1 className="text-xl font-semibold tracking-tight text-fg">{title}</h1>
          {subtitle && (
            <div className="mt-1 text-sm text-fg-muted">{subtitle}</div>
          )}
        </div>
        {actions && (
          <div className="flex shrink-0 items-center gap-2">{actions}</div>
        )}
      </div>
    </header>
  );
}
