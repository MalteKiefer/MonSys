import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { FormEvent, useEffect, useState } from "react";

import { api, ApiError } from "../lib/api";
import { PasswordPolicy } from "../lib/types";
import { Card } from "./Profile";

// TODO(theme): this page still uses raw `zinc-*` Tailwind classes which
// don't follow the dark/light palette. Migrate to semantic tokens
// (text-fg-muted, bg-panel, border-border, …) in a follow-up.

export function AdminSecurity() {
  const qc = useQueryClient();
  const policy = useQuery({
    queryKey: ["password-policy"],
    queryFn: () => api<PasswordPolicy>("/v1/admin/security/password-policy"),
  });

  const [draft, setDraft] = useState<PasswordPolicy | null>(null);
  useEffect(() => {
    if (policy.data) setDraft(policy.data);
  }, [policy.data]);

  const [msg, setMsg] = useState<{ kind: "ok" | "err"; text: string } | null>(null);

  const save = useMutation({
    mutationFn: (next: PasswordPolicy) =>
      api<PasswordPolicy>("/v1/admin/security/password-policy", {
        method: "PUT",
        body: JSON.stringify(next),
      }),
    onSuccess: () => {
      setMsg({ kind: "ok", text: "Policy updated." });
      qc.invalidateQueries({ queryKey: ["password-policy"] });
    },
    onError: (err) => setMsg({ kind: "err", text: err instanceof ApiError ? err.detail : "failed" }),
  });

  if (policy.isLoading || !draft) return <p className="p-6 text-sm text-zinc-400">Loading…</p>;

  const set =
    <K extends keyof PasswordPolicy>(key: K) =>
    (value: PasswordPolicy[K]) =>
      setDraft({ ...draft, [key]: value });

  function submit(e: FormEvent) {
    e.preventDefault();
    save.mutate(draft!);
  }

  return (
    <div className="mx-auto max-w-3xl space-y-6 p-6">
      <header>
        <h2 className="text-lg font-semibold">Security</h2>
        <p className="text-sm text-zinc-400">Password requirements applied to all new and changed passwords.</p>
      </header>

      <Card title="Password policy">
        <form onSubmit={submit} className="space-y-4">
          <label className="block">
            <span className="text-xs text-zinc-400">Minimum length</span>
            <input
              type="number"
              min={4}
              max={128}
              value={draft.min_length}
              onChange={(e) => set("min_length")(parseInt(e.target.value || "0", 10))}
              className="mt-1 w-32 rounded border border-zinc-700 bg-zinc-950 px-3 py-1.5 text-sm focus:border-zinc-500 focus:outline-none"
            />
          </label>

          <fieldset className="grid grid-cols-2 gap-2 text-sm text-zinc-300">
            <Toggle label="Uppercase letter" value={draft.require_upper} onChange={set("require_upper")} />
            <Toggle label="Lowercase letter" value={draft.require_lower} onChange={set("require_lower")} />
            <Toggle label="Digit" value={draft.require_digit} onChange={set("require_digit")} />
            <Toggle label="Symbol" value={draft.require_symbol} onChange={set("require_symbol")} />
          </fieldset>

          <label className="block">
            <span className="text-xs text-zinc-400">
              Max age (days, 0 = no expiry)
            </span>
            <input
              type="number"
              min={0}
              value={draft.max_age_days}
              onChange={(e) => set("max_age_days")(parseInt(e.target.value || "0", 10))}
              className="mt-1 w-32 rounded border border-zinc-700 bg-zinc-950 px-3 py-1.5 text-sm focus:border-zinc-500 focus:outline-none"
            />
          </label>

          <button
            type="submit"
            disabled={save.isPending}
            className="rounded bg-zinc-100 px-3 py-1.5 text-sm font-medium text-zinc-900 hover:bg-white disabled:opacity-50"
          >
            {save.isPending ? "Saving…" : "Save policy"}
          </button>
          {msg && (
            <p
              className={`rounded px-3 py-2 text-sm ${
                msg.kind === "ok"
                  ? "border border-ok/40 bg-ok/10 text-ok"
                  : "border border-fail/40 bg-fail/10 text-fail"
              }`}
            >
              {msg.text}
            </p>
          )}
        </form>
      </Card>
    </div>
  );
}

function Toggle(props: { label: string; value: boolean; onChange: (v: boolean) => void }) {
  return (
    <label className="flex items-center gap-2">
      <input
        type="checkbox"
        checked={props.value}
        onChange={(e) => props.onChange(e.target.checked)}
      />
      {props.label}
    </label>
  );
}
