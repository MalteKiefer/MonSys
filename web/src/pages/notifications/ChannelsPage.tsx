import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { PencilLine, Plus, Send, Trash2 } from "lucide-react";
import { useState } from "react";

import { Page } from "../../components/page";
import {
  Button,
  ErrorBox,
  Panel,
  PanelBody,
  PanelHeader,
  StatusPill,
  SuccessBox,
  TBody,
  TD,
  TH,
  THead,
  Table,
} from "../../components/ui";
import { useT } from "../../i18n/useT";
import { api, ApiError } from "../../lib/api";
import { useAuth } from "../../lib/auth";
import { NotificationChannel } from "../../lib/types";

import { ChannelForm } from "./ChannelForm";
import { NotificationsTabs } from "./NotificationsTabs";

// /notifications/channels — channels are user-scoped; the server filters its
// list to the caller's channels (plus shared ones admins can see). We don't
// re-enforce admin here; admin-only behaviour (edit anyone's channel) is
// driven by the row's owner check below.

export function ChannelsPage() {
  const { t } = useT(["notifications", "common"]);
  const user = useAuth((s) => s.user);
  const isAdmin = user?.role === "admin";
  const myID = user?.id ?? "";

  const qc = useQueryClient();
  const list = useQuery({
    queryKey: ["channels"],
    queryFn: () => api<{ channels: NotificationChannel[] }>("/v1/notifications/channels"),
  });

  const [editing, setEditing] = useState<NotificationChannel | null>(null);
  const [creating, setCreating] = useState(false);

  return (
    <Page
      title={t("notifications:channels.page_title")}
      subtitle={t("notifications:channels.page_subtitle")}
      actions={<NotificationsTabs />}
    >
      {(creating || editing) && (
        <ChannelForm
          initial={editing}
          isAdmin={!!isAdmin}
          onCancel={() => {
            setEditing(null);
            setCreating(false);
          }}
          onSaved={() => {
            void qc.invalidateQueries({ queryKey: ["channels"] });
            setEditing(null);
            setCreating(false);
          }}
        />
      )}

      <Panel>
        <PanelHeader>
          <h3 className="text-sm font-semibold">{t("notifications:channels.panel_title")}</h3>
          <Button variant="primary" onClick={() => setCreating(true)}>
            <Plus className="h-3.5 w-3.5" /> {t("notifications:channels.new_channel")}
          </Button>
        </PanelHeader>
        <PanelBody className="p-0 overflow-x-auto">
          {list.isLoading ? (
            <p className="px-5 py-4 text-sm text-fg-subtle">{t("common:actions.loading")}</p>
          ) : list.error ? (
            <ErrorBox>{(list.error as Error).message}</ErrorBox>
          ) : (list.data?.channels ?? []).length === 0 ? (
            <p className="px-5 py-8 text-center text-sm text-fg-subtle">{t("notifications:channels.empty")}</p>
          ) : (
            <Table>
              <THead>
                <tr>
                  <TH>{t("notifications:channels.table.type")}</TH>
                  <TH>{t("notifications:channels.table.name")}</TH>
                  <TH>{t("notifications:channels.table.owner")}</TH>
                  <TH>{t("notifications:channels.table.enabled")}</TH>
                  <TH>{t("notifications:channels.table.last_used")}</TH>
                  <TH>{t("notifications:channels.table.last_error")}</TH>
                  <TH className="text-right">{t("notifications:channels.table.actions")}</TH>
                </tr>
              </THead>
              <TBody>
                {(list.data?.channels ?? []).map((c) => (
                  <ChannelRow
                    key={c.id}
                    channel={c}
                    isAdmin={!!isAdmin}
                    myID={myID}
                    onEdit={() => setEditing(c)}
                    onChange={() => { void qc.invalidateQueries({ queryKey: ["channels"] }); }}
                  />
                ))}
              </TBody>
            </Table>
          )}
        </PanelBody>
      </Panel>
    </Page>
  );
}

function ChannelRow({
  channel,
  isAdmin,
  myID,
  onEdit,
  onChange,
}: {
  channel: NotificationChannel;
  isAdmin: boolean;
  myID: string;
  onEdit: () => void;
  onChange: () => void;
}) {
  const { t } = useT(["notifications", "common"]);
  const ownedByMe = channel.owner_user_id === myID;
  const canEdit = isAdmin || ownedByMe;
  const ownerLabel = !channel.owner_user_id
    ? "shared"
    : ownedByMe
      ? "you"
      : "other";
  const ownerText = ownerLabel === "shared"
    ? t("notifications:channels.owner.shared")
    : ownerLabel === "you"
      ? t("notifications:channels.owner.you")
      : t("notifications:channels.owner.other");
  const [testMsg, setTestMsg] = useState<{ kind: "ok" | "err"; text: string } | null>(null);

  const sendTest = useMutation({
    mutationFn: () =>
      api<{ ok: boolean; error?: string }>(`/v1/notifications/channels/${channel.id}/test`, {
        method: "POST",
        body: JSON.stringify({}),
      }),
    onSuccess: (data) => {
      if (data.ok) setTestMsg({ kind: "ok", text: t("notifications:channels.test_success") });
      else setTestMsg({ kind: "err", text: data.error ?? t("notifications:channels.test_failed") });
    },
    onError: (err) => setTestMsg({ kind: "err", text: err instanceof ApiError ? err.detail : t("notifications:channels.test_generic_failed") }),
  });

  const del = useMutation({
    mutationFn: () => api(`/v1/notifications/channels/${channel.id}`, { method: "DELETE" }),
    onSuccess: onChange,
  });

  return (
    <>
      <tr className="hover:bg-panel-2">
        <TD className="text-fg-muted">{channel.type}</TD>
        <TD className="font-medium">{channel.name}</TD>
        <TD>
          <span className={`inline-flex rounded-md px-1.5 py-0.5 text-[11px] font-medium ${
            ownerLabel === "shared"
              ? "bg-info/10 text-info ring-1 ring-inset ring-info/30"
              : ownerLabel === "you"
                ? "bg-accent/10 text-accent ring-1 ring-inset ring-accent/30"
                : "bg-panel-2 text-fg-subtle ring-1 ring-inset ring-border"
          }`}>{ownerText}</span>
        </TD>
        <TD>
          <StatusPill status={channel.enabled ? "ok" : "offline"}>
            {channel.enabled ? t("notifications:channels.state_on") : t("notifications:channels.state_off")}
          </StatusPill>
        </TD>
        <TD className="text-fg-muted">
          {channel.last_used_at ? new Date(channel.last_used_at).toLocaleString() : "—"}
        </TD>
        <TD className="font-mono text-xs text-fg-subtle truncate max-w-xs">
          {channel.last_error || "—"}
        </TD>
        <TD className="text-right">
          <div className="inline-flex items-center gap-1">
            <Button onClick={() => sendTest.mutate()} disabled={sendTest.isPending || !canEdit}>
              <Send className="h-3.5 w-3.5" /> {t("notifications:channels.actions.test")}
            </Button>
            <Button onClick={onEdit} disabled={!canEdit}>
              <PencilLine className="h-3.5 w-3.5" /> {t("notifications:channels.actions.edit")}
            </Button>
            <Button
              variant="danger"
              disabled={!canEdit}
              onClick={() => {
                if (confirm(t("notifications:channels.confirm_delete", { name: channel.name }))) del.mutate();
              }}
            >
              <Trash2 className="h-3.5 w-3.5" />
            </Button>
          </div>
        </TD>
      </tr>
      {testMsg && (
        <tr className="bg-bg">
          <td colSpan={7} className="px-3 py-2 text-xs">
            {testMsg.kind === "ok" ? <SuccessBox>{testMsg.text}</SuccessBox> : <ErrorBox>{testMsg.text}</ErrorBox>}
          </td>
        </tr>
      )}
    </>
  );
}
