// Step 2 — Scope. Single radio between "all / tags / groups / specific
// hosts", with the matching searchable multi-select underneath. The hosts/
// tags/groups data is fetched by the parent wizard so this component stays
// presentational.

import { useMemo, useState } from "react";

import { TagChip } from "../../../components/notifications/FormParts";
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
  tags: Array<{ tag: string; count: number }>;
  hosts: Host[];
  groups: HostGroup[];
}) {
  const [tagSearch, setTagSearch] = useState("");
  const [groupSearch, setGroupSearch] = useState("");
  const [hostSearch, setHostSearch] = useState("");

  const tagOptions = useMemo(
    () =>
      tags.map((t) => ({
        id: t.tag,
        label: t.tag,
        sub: `${t.count} host${t.count === 1 ? "" : "s"}`,
      })),
    [tags],
  );

  const hostOptions = useMemo(
    () =>
      hosts.map((h) => ({
        id: h.id,
        label: hostDisplay(h),
        sub: (h.tags ?? []).length > 0 ? (h.tags ?? []).map((t) => `#${t}`).join(" ") : "no tags",
      })),
    [hosts],
  );

  const groupOptions = useMemo(
    () =>
      groups.map((g) => ({
        id: g.id,
        label: g.name,
        sub: `${g.member_ids.length} member${g.member_ids.length === 1 ? "" : "s"}`,
      })),
    [groups],
  );

  const choices: Array<{ value: RuleDraft["targetMode"]; label: string; hint: string }> = [
    { value: "all", label: "All hosts", hint: "Every host that reports to MonSys." },
    { value: "tags", label: "Hosts with tag…", hint: "Match any selected tag." },
    { value: "groups", label: "Hosts in group…", hint: "Match any selected host group." },
    { value: "hosts", label: "Specific hosts", hint: "Pick individual hosts." },
  ];

  return (
    <div className="space-y-4">
      <fieldset>
        <legend className="mb-2 text-[11px] font-semibold uppercase tracking-[0.08em] text-fg-subtle">
          Who does this rule watch?
        </legend>
        <div className="space-y-1.5">
          {choices.map((c) => {
            const active = draft.targetMode === c.value;
            return (
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
            Tags ({draft.targetTags.length} selected)
          </p>
          <MultiSelectList
            items={tagOptions}
            selected={draft.targetTags}
            onToggle={(id) =>
              patch({
                targetTags: draft.targetTags.includes(id)
                  ? draft.targetTags.filter((t) => t !== id)
                  : [...draft.targetTags, id],
              })
            }
            empty={tagOptions.length === 0 ? "No tags defined yet." : "No matching tags."}
            search={tagSearch}
            onSearch={setTagSearch}
            placeholder="Search tags…"
          />
          {draft.targetTags.length > 0 && (
            <div className="mt-2 flex flex-wrap gap-1">
              {draft.targetTags.map((t) => (
                <TagChip
                  key={t}
                  text={t}
                  onRemove={() =>
                    patch({ targetTags: draft.targetTags.filter((x) => x !== t) })
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
            Groups ({draft.targetGroupIds.length} selected)
          </p>
          <MultiSelectList
            items={groupOptions}
            selected={draft.targetGroupIds}
            onToggle={(id) =>
              patch({
                targetGroupIds: draft.targetGroupIds.includes(id)
                  ? draft.targetGroupIds.filter((g) => g !== id)
                  : [...draft.targetGroupIds, id],
              })
            }
            empty={groupOptions.length === 0 ? "No groups defined yet." : "No matching groups."}
            search={groupSearch}
            onSearch={setGroupSearch}
            placeholder="Search groups…"
          />
        </section>
      )}

      {draft.targetMode === "hosts" && (
        <section>
          <p className="mb-2 text-[11px] font-semibold uppercase tracking-[0.08em] text-fg-subtle">
            Hosts ({draft.targetHostIds.length} selected)
          </p>
          <MultiSelectList
            items={hostOptions}
            selected={draft.targetHostIds}
            onToggle={(id) =>
              patch({
                targetHostIds: draft.targetHostIds.includes(id)
                  ? draft.targetHostIds.filter((h) => h !== id)
                  : [...draft.targetHostIds, id],
              })
            }
            empty={hostOptions.length === 0 ? "No hosts known yet." : "No matching hosts."}
            search={hostSearch}
            onSearch={setHostSearch}
            placeholder="Search hosts…"
          />
        </section>
      )}
    </div>
  );
}
