// Step 2 — Scope. Single radio between "all / tags / groups / specific
// hosts", with the matching searchable multi-select underneath. The hosts/
// tags/groups data is fetched by the parent wizard so this component stays
// presentational.

import { useMemo, useState } from "react";

import { TagChip } from "../../../components/notifications/FormParts";
import { useT } from "../../../i18n/useT";
import { hostDisplay } from "../../../lib/utils";
import type { Host, HostGroup } from "../../../lib/types";

import type { RuleDraft } from "./draft";
import { MultiSelectList } from "./MultiSelectList";

export function StepScope({
  draft,
  patch,
  tags,
  hosts,
  groups,
}: {
  draft: RuleDraft;
  patch: (p: Partial<RuleDraft>) => void;
  tags: { tag: string; count: number }[];
  hosts: Host[];
  groups: HostGroup[];
}) {
  const { t } = useT(["notifications", "common"]);
  const [tagSearch, setTagSearch] = useState("");
  const [groupSearch, setGroupSearch] = useState("");
  const [hostSearch, setHostSearch] = useState("");

  const tagOptions = useMemo(
    () =>
      tags.map((tg) => ({
        id: tg.tag,
        label: tg.tag,
        sub: t("notifications:wizard.scope.tags_count", { count: tg.count }),
      })),
    [tags, t],
  );

  const hostOptions = useMemo(
    () =>
      hosts.map((h) => ({
        id: h.id,
        label: hostDisplay(h),
        sub: (h.tags ?? []).length > 0
          ? (h.tags ?? []).map((tg) => `#${tg}`).join(" ")
          : t("notifications:wizard.scope.host_no_tags"),
      })),
    [hosts, t],
  );

  const groupOptions = useMemo(
    () =>
      groups.map((g) => ({
        id: g.id,
        label: g.name,
        sub: t("notifications:wizard.scope.groups_members", { count: g.member_ids.length }),
      })),
    [groups, t],
  );

  const choices: { value: RuleDraft["targetMode"]; label: string; hint: string }[] = [
    { value: "all", label: t("notifications:wizard.scope.choices.all_label"), hint: t("notifications:wizard.scope.choices.all_hint") },
    { value: "tags", label: t("notifications:wizard.scope.choices.tags_label"), hint: t("notifications:wizard.scope.choices.tags_hint") },
    { value: "groups", label: t("notifications:wizard.scope.choices.groups_label"), hint: t("notifications:wizard.scope.choices.groups_hint") },
    { value: "hosts", label: t("notifications:wizard.scope.choices.hosts_label"), hint: t("notifications:wizard.scope.choices.hosts_hint") },
  ];

  return (
    <div className="space-y-4">
      <fieldset>
        <legend className="mb-2 text-[11px] font-semibold uppercase tracking-[0.08em] text-fg-subtle">
          {t("notifications:wizard.scope.legend")}
        </legend>
        <div className="space-y-1.5">
          {choices.map((c) => {
            const active = draft.targetMode === c.value;
            return (
              /* eslint-disable-next-line jsx-a11y/label-has-associated-control -- the nested <input type=radio> + visible <span> label IS the association; rule doesn't detect the implicit form-element wrap. */
              <label
                key={c.value}
                className={`flex cursor-pointer items-start gap-3 rounded-md border px-3 py-2 transition-colors duration-150 ${
                  active
                    ? "border-accent bg-accent/5"
                    : "border-border bg-panel-2/40 hover:bg-panel-2 hover:border-border-strong"
                }`}
              >
                <input
                  type="radio"
                  name="target-mode"
                  className="mt-1 h-3.5 w-3.5 accent-accent"
                  checked={active}
                  onChange={() => {
                    // Switching mode wipes the lists for the modes we're not
                    // using so we don't carry stale selections through Save.
                    patch({
                      targetMode: c.value,
                      targetTags: c.value === "tags" ? draft.targetTags : [],
                      targetGroupIds: c.value === "groups" ? draft.targetGroupIds : [],
                      targetHostIds: c.value === "hosts" ? draft.targetHostIds : [],
                    });
                  }}
                />
                <span className="min-w-0 flex-1">
                  <span className="block text-sm font-medium text-fg">{c.label}</span>
                  <span className="mt-0.5 block text-[11px] text-fg-subtle">{c.hint}</span>
                </span>
              </label>
            );
          })}
        </div>
      </fieldset>

      {draft.targetMode === "tags" && (
        <section>
          <p className="mb-2 text-[11px] font-semibold uppercase tracking-[0.08em] text-fg-subtle">
            {t("notifications:wizard.scope.tags_header", { count: draft.targetTags.length })}
          </p>
          <MultiSelectList
            items={tagOptions}
            selected={draft.targetTags}
            onToggle={(id) =>
              { patch({
                targetTags: draft.targetTags.includes(id)
                  ? draft.targetTags.filter((tg) => tg !== id)
                  : [...draft.targetTags, id],
              }); }
            }
            empty={tagOptions.length === 0
              ? t("notifications:wizard.scope.tags_empty_none")
              : t("notifications:wizard.scope.tags_empty_match")}
            search={tagSearch}
            onSearch={setTagSearch}
            placeholder={t("notifications:wizard.scope.tags_search_placeholder")}
          />
          {draft.targetTags.length > 0 && (
            <div className="mt-2 flex flex-wrap gap-1">
              {draft.targetTags.map((tg) => (
                <TagChip
                  key={tg}
                  text={tg}
                  onRemove={() =>
                    { patch({ targetTags: draft.targetTags.filter((x) => x !== tg) }); }
                  }
                />
              ))}
            </div>
          )}
        </section>
      )}

      {draft.targetMode === "groups" && (
        <section>
          <p className="mb-2 text-[11px] font-semibold uppercase tracking-[0.08em] text-fg-subtle">
            {t("notifications:wizard.scope.groups_header", { count: draft.targetGroupIds.length })}
          </p>
          <MultiSelectList
            items={groupOptions}
            selected={draft.targetGroupIds}
            onToggle={(id) =>
              { patch({
                targetGroupIds: draft.targetGroupIds.includes(id)
                  ? draft.targetGroupIds.filter((g) => g !== id)
                  : [...draft.targetGroupIds, id],
              }); }
            }
            empty={groupOptions.length === 0
              ? t("notifications:wizard.scope.groups_empty_none")
              : t("notifications:wizard.scope.groups_empty_match")}
            search={groupSearch}
            onSearch={setGroupSearch}
            placeholder={t("notifications:wizard.scope.groups_search_placeholder")}
          />
        </section>
      )}

      {draft.targetMode === "hosts" && (
        <section>
          <p className="mb-2 text-[11px] font-semibold uppercase tracking-[0.08em] text-fg-subtle">
            {t("notifications:wizard.scope.hosts_header", { count: draft.targetHostIds.length })}
          </p>
          <MultiSelectList
            items={hostOptions}
            selected={draft.targetHostIds}
            onToggle={(id) =>
              { patch({
                targetHostIds: draft.targetHostIds.includes(id)
                  ? draft.targetHostIds.filter((h) => h !== id)
                  : [...draft.targetHostIds, id],
              }); }
            }
            empty={hostOptions.length === 0
              ? t("notifications:wizard.scope.hosts_empty_none")
              : t("notifications:wizard.scope.hosts_empty_match")}
            search={hostSearch}
            onSearch={setHostSearch}
            placeholder={t("notifications:wizard.scope.hosts_search_placeholder")}
          />
        </section>
      )}
    </div>
  );
}
