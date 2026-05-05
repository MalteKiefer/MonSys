import { ReactNode } from "react";

import { BreadcrumbItem } from "./Breadcrumb";
import { PageHeader } from "./PageHeader";

// Unified page shell. Wraps every routed view in a consistent container so
// pages don't each re-derive the same `mx-auto max-w-7xl …` boilerplate, and
// renders the standard header (breadcrumb + H1 + subtitle + actions) above
// the body. The caller decides the body layout.
export function Page({
  title,
  subtitle,
  breadcrumb,
  actions,
  children,
}: {
  title: ReactNode;
  subtitle?: ReactNode;
  breadcrumb?: BreadcrumbItem[];
  actions?: ReactNode;
  children: ReactNode;
}) {
  return (
    <div className="mx-auto max-w-7xl space-y-4 p-4 sm:p-6">
      <PageHeader
        title={title}
        subtitle={subtitle}
        breadcrumb={breadcrumb}
        actions={actions}
      />
      {children}
    </div>
  );
}
