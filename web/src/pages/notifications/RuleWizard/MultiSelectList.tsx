// Reusable searchable multi-checkbox list. Shared by StepScope (hosts/tags/
// groups) and StepNotify (channels). Keeps the panel-width interaction
// compact so the live-preview sidebar still fits to the right.

import { Search } from "lucide-react";
import { useMemo, type ReactNode } from "react";

import { TextInput } from "../../../components/ui";

export type MultiSelectItem = { id: string; label: string; sub?: ReactNode };

export function MultiSelectList({
  items,
  selected,
  onToggle,
  empty,
  search,
  onSearch,
  placeholder,
}: {
  items: MultiSelectItem[];
  selected: string[];
  onToggle: (id: string) => void;
  empty: string;
  search: string;
  onSearch: (v: string) => void;
  placeholder: string;
}) {
  const filtered = useMemo(() => {
    const q = search.trim().toLowerCase();
    if (!q) return items;
    return items.filter((it) => it.label.toLowerCase().includes(q));
  }, [items, search]);
  return (
    <div className="space-y-2">
      <div className="relative">
        <Search className="pointer-events-none absolute left-2 top-1/2 h-3.5 w-3.5 -translate-y-1/2 text-fg-subtle" />
        <TextInput
          value={search}
          onChange={(e) => onSearch(e.target.value)}
          placeholder={placeholder}
          className="pl-7"
        />
      </div>
      <div className="max-h-56 overflow-y-auto rounded-md border border-border bg-panel-2/40 p-1">
        {filtered.length === 0 ? (
          <p className="px-2 py-3 text-center text-xs text-fg-subtle">{empty}</p>
        ) : (
          <ul className="space-y-0.5">
            {filtered.map((it) => {
              const checked = selected.includes(it.id);
              return (
                <li key={it.id}>
                  <label
                    className={`flex cursor-pointer items-start gap-2 rounded-md border px-2 py-1.5 text-sm transition-colors duration-150 ${
                      checked
                        ? "border-accent/60 bg-accent/10"
                        : "border-transparent hover:bg-panel-2"
                    }`}
                  >
                    <input
                      type="checkbox"
                      checked={checked}
                      onChange={() => onToggle(it.id)}
                      className="mt-0.5 h-3.5 w-3.5 accent-accent"
                    />
                    <span className="min-w-0 flex-1">
                      <span className="block truncate text-xs font-medium text-fg">{it.label}</span>
                      {it.sub && <span className="mt-0.5 block text-[11px] text-fg-subtle">{it.sub}</span>}
                    </span>
                  </label>
                </li>
              );
            })}
          </ul>
        )}
      </div>
    </div>
  );
}
