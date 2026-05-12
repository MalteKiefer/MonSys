import { KeyRound } from "lucide-react";
import { FormEvent, useState } from "react";
import { useNavigate, useSearchParams } from "react-router-dom";

import { Button, ErrorBox, Field, Panel, SuccessBox, TextInput } from "../components/ui";
import { useT } from "../i18n/useT";
import { api, ApiError } from "../lib/api";

// TODO(theme): this page still uses raw `zinc-*` Tailwind classes which
// don't follow the dark/light palette. Migrate to semantic tokens
// (text-fg-muted, bg-panel, border-border, …) in a follow-up.

export function Reset() {
  const { t } = useT("auth");
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
        <div className="w-full max-w-sm">
          <ErrorBox>{t("reset.error_missing_token")}</ErrorBox>
        </div>
      </div>
    );
  }

  async function submit(e: FormEvent) {
    e.preventDefault();
    setError(null);
    if (pw !== pw2) {
      setError(t("reset.error_mismatch"));
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
      setError(err instanceof ApiError ? err.detail : t("reset.error_failed"));
    } finally {
      setBusy(false);
    }
  }

  if (done) {
    return (
      <div className="flex min-h-full items-center justify-center px-4 py-10">
        <Panel className="w-full max-w-sm">
          <div className="space-y-4 p-6">
            <SuccessBox>{t("reset.success")}</SuccessBox>
            <Button
              variant="primary"
              onClick={() => navigate("/login", { replace: true })}
              className="w-full"
            >
              {t("reset.go_to_login")}
            </Button>
          </div>
        </Panel>
      </div>
    );
  }

  return (
    <div className="flex min-h-full items-center justify-center px-4 py-10">
      <Panel className="w-full max-w-sm">
        <form onSubmit={submit} className="space-y-5 p-6">
          <div className="flex items-center gap-2">
            <KeyRound className="h-5 w-5 text-accent" />
            <h1 className="text-lg font-semibold">{t("reset.title")}</h1>
          </div>
          <p className="text-sm text-fg-muted">
            {t("reset.hint")}
          </p>
          <Field label={t("reset.new_password")}>
            <TextInput
              type="password"
              required
              value={pw}
              onChange={(e) => setPw(e.target.value)}
            />
          </Field>
          <Field label={t("reset.confirm")}>
            <TextInput
              type="password"
              required
              value={pw2}
              onChange={(e) => setPw2(e.target.value)}
            />
          </Field>
          {error && <ErrorBox>{error}</ErrorBox>}
          <Button type="submit" variant="primary" disabled={busy} className="w-full">
            {busy ? t("reset.submitting") : t("reset.submit")}
          </Button>
        </form>
      </Panel>
    </div>
  );
}
