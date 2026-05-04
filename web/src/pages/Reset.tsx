import { FormEvent, useState } from "react";
import { useNavigate, useSearchParams } from "react-router-dom";

import { api, ApiError } from "../lib/api";

// TODO(theme): this page still uses raw `zinc-*` Tailwind classes which
// don't follow the dark/light palette. Migrate to semantic tokens
// (text-fg-muted, bg-panel, border-border, …) in a follow-up.

export function Reset() {
  const [params] = useSearchParams();
  const navigate = useNavigate();
  const token = params.get("token") ?? "";
  const [pw, setPw] = useState("");
  const [pw2, setPw2] = useState("");
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState<string | null>(null);
  const [done, setDone] = useState(false);

  if (!token) {
    return (
      <div className="flex min-h-full items-center justify-center px-4">
        <p className="rounded border border-fail/40 bg-fail/10 px-4 py-3 text-sm text-fail">
          Missing reset token. Use the link from your invite email.
        </p>
      </div>
    );
  }

  async function submit(e: FormEvent) {
    e.preventDefault();
    setError(null);
    if (pw !== pw2) {
      setError("Passwords do not match.");
      return;
    }
    setBusy(true);
    try {
      await api<{ ok: boolean }>("/v1/auth/consume-reset", {
        method: "POST",
        skipAuth: true,
        body: JSON.stringify({ token, new_password: pw }),
      });
      setDone(true);
    } catch (err) {
      setError(err instanceof ApiError ? err.detail : "failed");
    } finally {
      setBusy(false);
    }
  }

  if (done) {
    return (
      <div className="flex min-h-full items-center justify-center px-4">
        <div className="w-full max-w-sm space-y-3 rounded-lg border border-ok/40 bg-ok/10 p-6">
          <h2 className="text-base font-semibold text-ok">Password set</h2>
          <p className="text-sm text-zinc-300">
            You can sign in now.
          </p>
          <button
            onClick={() => navigate("/login", { replace: true })}
            className="rounded bg-zinc-100 px-3 py-1.5 text-sm font-medium text-zinc-900 hover:bg-white"
          >
            Go to login
          </button>
        </div>
      </div>
    );
  }

  return (
    <div className="flex min-h-full items-center justify-center px-4">
      <form
        onSubmit={submit}
        className="w-full max-w-sm space-y-3 rounded-lg border border-zinc-800 bg-zinc-900 p-6"
      >
        <h2 className="text-base font-semibold">Set your password</h2>
        <p className="text-sm text-zinc-400">
          Choose a strong password. Server policy will reject weak ones.
        </p>
        <label className="block">
          <span className="text-xs text-zinc-400">New password</span>
          <input
            type="password"
            required
            value={pw}
            onChange={(e) => setPw(e.target.value)}
            className="mt-1 w-full rounded border border-zinc-700 bg-zinc-950 px-3 py-2 text-sm focus:border-zinc-500 focus:outline-none"
          />
        </label>
        <label className="block">
          <span className="text-xs text-zinc-400">Confirm</span>
          <input
            type="password"
            required
            value={pw2}
            onChange={(e) => setPw2(e.target.value)}
            className="mt-1 w-full rounded border border-zinc-700 bg-zinc-950 px-3 py-2 text-sm focus:border-zinc-500 focus:outline-none"
          />
        </label>
        {error && (
          <p className="rounded border border-fail/40 bg-fail/10 px-3 py-2 text-sm text-fail">
            {error}
          </p>
        )}
        <button
          type="submit"
          disabled={busy}
          className="w-full rounded bg-zinc-100 py-2 text-sm font-medium text-zinc-900 hover:bg-white disabled:opacity-50"
        >
          {busy ? "Saving…" : "Set password"}
        </button>
      </form>
    </div>
  );
}
