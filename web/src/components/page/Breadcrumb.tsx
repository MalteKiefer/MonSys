import { ChevronRight } from "lucide-react";
import { Fragment } from "react";
import { Link } from "react-router-dom";

// One crumb. The last crumb in a breadcrumb trail is rendered as plain text
// (it represents the current page) — leave `to` undefined for that one.
export interface BreadcrumbItem {
  label: string;
  to?: string;
}

// Chevron-separated trail rendered above a page heading. Stays small and
// subtle (`text-xs text-fg-subtle`) so it doesn't compete with the H1 below.
// Only renders the items it's given — no implicit "Home" prefix — to keep the
// component predictable across pages.
export function Breadcrumb({ items }: { items: BreadcrumbItem[] }) {
  if (items.length === 0) return null;
  return (
    <nav aria-label="Breadcrumb" className="text-xs text-fg-subtle">
      <ol className="flex flex-wrap items-center gap-1">
        {items.map((item, idx) => {
          const isLast = idx === items.length - 1;
          return (
            <Fragment key={`${item.label}-${idx}`}>
              <li>
                {item.to && !isLast ? (
                  <Link
                    to={item.to}
                    className="text-fg-subtle transition-colors hover:text-fg"
                  >
                    {item.label}
                  </Link>
                ) : (
                  <span aria-current={isLast ? "page" : undefined}>{item.label}</span>
                )}
              </li>
              {!isLast && (
                <li aria-hidden className="flex items-center text-fg-subtle">
                  <ChevronRight className="h-3 w-3" />
                </li>
              )}
            </Fragment>
          );
        })}
      </ol>
    </nav>
  );
}
