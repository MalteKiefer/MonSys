import { keepPreviousData, useQuery } from "@tanstack/react-query";
import { ClipboardList } from "lucide-react";
import { useEffect, useMemo, useState } from "react";

import { EmptyState, ErrorState, Page } from "../components/page";
import {
  Field,
  Panel,
  PanelBody,
  PanelHeader,
  TBody,
  TD,
  TH,
  THead,
  Table,
  TextInput,
} from "../components/ui";
import { api } from "../lib/api";
import { AuditEntry } from "../lib/types";

const PAGE_SIZE = 100;

type Resp = {
  entries: AuditEntry[];
  total: number;
};

export function AdminAudit() {
  const [actor, setActor] = useState("");
  const [action, setAction] = useState("");
  const [debouncedActor, setDebouncedActor] = useState("");
  const [debouncedAction, setDebouncedAction] = useState("");
  const [offset, setOffset] = useState(0);

  // Debounce filters so typing doesn't fire a request per keystroke.
  useEffect(() => {
    const t = setTimeout(() => setDebouncedActor(actor.trim()), 250);
    return () => clearTimeout(t);
  }, [actor]);
  useEffect(() => {
    const t = setTimeout(() => setDebouncedAction(action.trim()), 250);
    return () => clearTimeout(t);
  }, [action]);
  useEffect(() => setOffset(0), [debouncedActor, debouncedAction]);

  const params = useMemo(() => {
    const u = new URLSearchParams();
    if (debouncedActor) u.set("actor", debouncedActor);
    if (debouncedAction) u.set("action", debouncedAction);
    u.set("limit", String(PAGE_SIZE));
    u.set("offset", String(offset));
    return u.toString();
  }, [debouncedActor, debouncedAction, offset]);

  const audit = useQuery({
    queryKey: ["admin-audit", params],
    queryFn: () => api<Resp>(`/v1/admin/audit?${params}`),
    placeholderData: keepPreviousData,
  });

  const total = audit.data?.total ?? 0;
  const entries = audit.data?.entries ?? [];

  return (
    <Page
      title="Audit log"
      subtitle="Server-side record of admin-only actions: who changed what, when. Filter by exact actor email or action key."
      breadcrumb={[{ label: "Admin" }, { label: "Audit log" }]}
      actions={<span className="text-xs text-fg-subtle tabular-nums">{total} entries</span>}
    >
      <Panel>
        <PanelHeader>
          <div className="flex w-full flex-wrap items-end gap-3">
            <div className="min-w-[220px] flex-1">
              <Field label="Actor" hint="Exact match on the user's email">
                <TextInput
                  type="search"
                  placeholder="admin@example.com"
                  value={actor}
                  onChange={(e) => setActor(e.target.value)}
                />
              </Field>
            </div>
            <div className="min-w-[220px] flex-1">
              <Field label="Action" hint="e.g. user.create, channel.delete">
                <TextInput
                  type="search"
                  placeholder="user.create"
                  value={action}
                  onChange={(e) => setAction(e.target.value)}
                />
              </Field>
            </div>
          </div>
        </PanelHeader>
        <PanelBody className="p-0 overflow-x-auto">
          {audit.isLoading ? (
            <p className="px-5 py-4 text-sm text-fg-subtle">Loading…</p>
          ) : audit.error ? (
            <div className="p-5">
              <ErrorState
                message={(audit.error as Error).message}
                onRetry={() => audit.refetch()}
              />
            </div>
          ) : entries.length === 0 ? (
            <EmptyState
              icon={ClipboardList}
              title="No audit entries match."
              hint="Try clearing the actor or action filter, or check back after an admin action runs."
            />
          ) : (
            <Table aria-label="Audit log entries">
              <THead>
                <tr>
                  <TH>At</TH>
                  <TH>Actor</TH>
                  <TH>Action</TH>
                  <TH>Target</TH>
                  <TH>Detail</TH>
                </tr>
              </THead>
              <TBody>
                {entries.map((e) => (
                  <tr key={e.id} className="hover:bg-panel-2 align-top">
                    <TD className="font-mono text-[11px] text-fg-muted whitespace-nowrap">
                      {new Date(e.at).toLocaleTimeString()}
                      <span className="ml-2 text-fg-subtle">
                        {new Date(e.at).toLocaleDateString()}
                      </span>
                    </TD>
                    <TD className="font-mono text-xs">{e.actor || "—"}</TD>
                    <TD className="font-mono text-xs text-accent">{e.action}</TD>
                    <TD className="font-mono text-[11px] text-fg-muted break-all">
                      {e.target || "—"}
                    </TD>
                    <TD className="font-mono text-[11px] text-fg-muted break-all">
                      {formatDetail(e.detail)}
                    </TD>
                  </tr>
                ))}
              </TBody>
            </Table>
          )}
        </PanelBody>
        {total > PAGE_SIZE && (
          <div
            aria-live="polite"
            className="flex items-center justify-between border-t border-border px-5 py-3 text-xs text-fg-muted"
          >
            <span className="tabular-nums">
              {offset + 1}–{Math.min(offset + entries.length, total)} of {total}
            </span>
            <div className="flex items-center gap-2">
              <button
                aria-label="Previous page"
                disabled={offset === 0}
                onClick={() => setOffset(Math.max(0, offset - PAGE_SIZE))}
                className="rounded-md border border-border px-2 py-1 hover:bg-panel-2 disabled:opacity-40"
              >
                Prev
              </button>
              <button
                aria-label="Next page"
                disabled={offset + PAGE_SIZE >= total}
                onClick={() => setOffset(offset + PAGE_SIZE)}
                className="rounded-md border border-border px-2 py-1 hover:bg-panel-2 disabled:opacity-40"
              >
                Next
              </button>
            </div>
          </div>
        )}
      </Panel>
    </Page>
  );
}

// formatDetail unwraps the {"text":"..."} convention the server uses for
// non-JSON detail strings, falling back to raw JSON when the payload is
// already an object/array.
function formatDetail(raw: string): string {
  if (!raw) return "—";
  try {
    const parsed = JSON.parse(raw);
    if (parsed && typeof parsed === "object" && "text" in parsed && Object.keys(parsed).length === 1) {
      const t = (parsed as { text?: unknown }).text;
      return typeof t === "string" && t !== "" ? t : "—";
    }
    return raw;
  } catch {
    return raw;
  }
}
