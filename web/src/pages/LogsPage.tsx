import { AlertTriangle, FileJson, FileSearch } from "lucide-react";
import { ReactNode, useCallback, useMemo, useState } from "react";
import { useSearchParams } from "react-router-dom";

import { Page } from "../components/page";
import { Tabs, TabItem } from "../components/ui";
import { AdminAuditContent } from "./AdminAudit";
import { AdminIngestsContent } from "./AdminIngests";
import { AdminLogsContent } from "./AdminLogs";

// Consolidated diagnostics page. The three former /admin/{logs,ingests,audit}
// views are mounted as tab panels here, with the active tab persisted to the
// URL via `?tab=` so existing deep links keep working (App.tsx adds Navigate
// redirects from the old paths that append the right query parameter).
//
// Only the active tab's content component is mounted at any time — this keeps
// each panel's queries scoped to "is this tab visible?" without us having to
// thread an `enabled` flag into the existing hooks.

type TabKey = "server" | "ingest" | "audit";

const TABS: ReadonlyArray<TabItem<TabKey>> = [
  { key: "server", label: "Server", icon: FileSearch },
  { key: "ingest", label: "Ingest", icon: FileJson },
  { key: "audit", label: "Audit", icon: AlertTriangle },
];

const SUBTITLES: Record<TabKey, string> = {
  server:
    "In-memory ring buffer of server-side log entries. Older entries roll off — ship the JSON stream off-host for retention.",
  ingest:
    "Recent agent ingest payloads. Re-marshalled JSON; semantically identical to what the agent sent.",
  audit:
    "Server-side record of admin-only actions: who changed what, when. Filter by exact actor email or action key.",
};

const TITLES: Record<TabKey, string> = {
  server: "Server logs",
  ingest: "Agent ingests",
  audit: "Audit log",
};

function isTabKey(v: string | null): v is TabKey {
  return v === "server" || v === "ingest" || v === "audit";
}

export function LogsPage() {
  const [search, setSearch] = useSearchParams();
  const raw = search.get("tab");
  const tab: TabKey = isTabKey(raw) ? raw : "server";

  // Each inner content component publishes a small "meta" node (count
  // badge, toolbar) via this callback so the consolidated page header can
  // host it. Memoized so the inner effect dependency stays stable.
  const [meta, setMeta] = useState<ReactNode>(null);
  const onMeta = useCallback((node: ReactNode) => setMeta(node), []);

  const setTab = useCallback(
    (next: TabKey) => {
      const params = new URLSearchParams(search);
      if (next === "server") params.delete("tab");
      else params.set("tab", next);
      // Clear stale meta from the outgoing panel; the incoming panel will
      // re-publish its own on mount.
      setMeta(null);
      setSearch(params, { replace: true });
    },
    [search, setSearch],
  );

  const body = useMemo(() => {
    switch (tab) {
      case "ingest":
        return <AdminIngestsContent onMeta={onMeta} />;
      case "audit":
        return <AdminAuditContent onMeta={onMeta} />;
      case "server":
      default:
        return <AdminLogsContent onMeta={onMeta} />;
    }
  }, [tab, onMeta]);

  return (
    <Page
      title={TITLES[tab]}
      subtitle={SUBTITLES[tab]}
      breadcrumb={[{ label: "Admin" }, { label: "Logs" }, { label: TITLES[tab] }]}
      actions={meta}
    >
      <Tabs items={TABS} value={tab} onChange={setTab} />
      <div className="mt-4">{body}</div>
    </Page>
  );
}
