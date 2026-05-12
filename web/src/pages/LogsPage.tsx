import { AlertTriangle, FileJson, FileSearch } from "lucide-react";
import { ReactNode, useCallback, useMemo, useState } from "react";
import { useSearchParams } from "react-router-dom";

import { Page } from "../components/page";
import { Tabs, TabItem } from "../components/ui";
import { useT } from "../i18n/useT";
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

function isTabKey(v: string | null): v is TabKey {
  return v === "server" || v === "ingest" || v === "audit";
}

export function LogsPage() {
  const { t } = useT(["admin", "common"]);
  const [search, setSearch] = useSearchParams();
  const raw = search.get("tab");
  const tab: TabKey = isTabKey(raw) ? raw : "server";

  const TABS: ReadonlyArray<TabItem<TabKey>> = [
    { key: "server", label: t("admin:logsPage.tab_server"), icon: FileSearch },
    { key: "ingest", label: t("admin:logsPage.tab_ingest"), icon: FileJson },
    { key: "audit", label: t("admin:logsPage.tab_audit"), icon: AlertTriangle },
  ];

  const SUBTITLES: Record<TabKey, string> = {
    server: t("admin:logsPage.subtitle_server"),
    ingest: t("admin:logsPage.subtitle_ingest"),
    audit: t("admin:logsPage.subtitle_audit"),
  };

  const TITLES: Record<TabKey, string> = {
    server: t("admin:logsPage.title_server"),
    ingest: t("admin:logsPage.title_ingest"),
    audit: t("admin:logsPage.title_audit"),
  };

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
      breadcrumb={[
        { label: t("admin:logsPage.breadcrumb_admin") },
        { label: t("admin:logsPage.breadcrumb_logs") },
        { label: TITLES[tab] },
      ]}
      actions={meta}
    >
      <Tabs items={TABS} value={tab} onChange={setTab} />
      <div className="mt-4">{body}</div>
    </Page>
  );
}
