import { CheckCircle2, MailWarning } from "lucide-react";
import { useEffect, useState } from "react";
import { Link, useNavigate, useSearchParams } from "react-router-dom";

import { Button, ErrorBox, Panel, SuccessBox } from "../components/ui";
import { useT } from "../i18n/useT";
import { api, ApiError } from "../lib/api";
import { useAuth } from "../lib/auth";

// Public landing page for the email-change confirmation link sent by
// POST /v1/auth/me/email/request. The link looks like
// /confirm-email?token=<opaque>. We POST the token to the public
// /v1/auth/email/confirm endpoint (no auth required) which updates the
// user's email AND revokes every session for that user — so we also flush
// the local zustand auth store on success and bounce the user to /login.

type State =
  | { kind: "loading" }
  | { kind: "ok" }
  | { kind: "err"; message: string }
  | { kind: "missing" };

export function ConfirmEmail() {
  const { t } = useT("auth");
  const [params] = useSearchParams();
  const navigate = useNavigate();
  const token = params.get("token") ?? "";
  const clearAuth = useAuth((s) => s.clear);
  const hasToken = !!token;
  const [state, setState] = useState<State>(hasToken ? { kind: "loading" } : { kind: "missing" });

  useEffect(() => {
    if (!hasToken) return;
    let cancelled = false;
    (async () => {
      try {
        await api<{ ok: boolean }>("/v1/auth/email/confirm", {
          method: "POST",
          skipAuth: true,
          body: JSON.stringify({ token }),
        });
        if (cancelled) return;
        // Server has already revoked every session for the user. Flush the
        // local store too so any cached token in this tab can't 401 us into
        // a confusing state — and so the post-login branch of App.tsx kicks
        // in when we navigate to /login.
        clearAuth();
        setState({ kind: "ok" });
      } catch (err) {
        if (cancelled) return;
        const msg = err instanceof ApiError ? err.detail : t("confirmEmail.err_default");
        setState({ kind: "err", message: msg });
      }
    })();
    return () => {
      cancelled = true;
    };
  }, [token, hasToken, clearAuth, t]);

  return (
    <div className="flex min-h-full items-center justify-center px-4 py-10">
      <Panel className="w-full max-w-sm">
        <div className="space-y-4 p-6">
          {state.kind === "missing" && (
            <>
              <div className="flex items-center gap-2">
                <MailWarning className="h-5 w-5 text-fail" />
                <h1 className="text-lg font-semibold">{t("confirmEmail.missing_title")}</h1>
              </div>
              <ErrorBox>
                {t("confirmEmail.missing_body")}
              </ErrorBox>
              <Button
                variant="primary"
                onClick={() => navigate("/login", { replace: true })}
                className="w-full"
              >
                {t("confirmEmail.go_to_login")}
              </Button>
            </>
          )}

          {state.kind === "loading" && (
            <>
              <h1 className="text-lg font-semibold">{t("confirmEmail.loading_title")}</h1>
              <p className="text-sm text-fg-muted">
                {t("confirmEmail.loading_body")}
              </p>
            </>
          )}

          {state.kind === "ok" && (
            <>
              <div className="flex items-center gap-2">
                <CheckCircle2 className="h-5 w-5 text-ok" />
                <h1 className="text-lg font-semibold">{t("confirmEmail.ok_title")}</h1>
              </div>
              <SuccessBox>
                {t("confirmEmail.ok_body")}
              </SuccessBox>
              <Button
                variant="primary"
                onClick={() => navigate("/login", { replace: true })}
                className="w-full"
              >
                {t("confirmEmail.go_to_login")}
              </Button>
            </>
          )}

          {state.kind === "err" && (
            <>
              <div className="flex items-center gap-2">
                <MailWarning className="h-5 w-5 text-fail" />
                <h1 className="text-lg font-semibold">{t("confirmEmail.err_title")}</h1>
              </div>
              <ErrorBox>{state.message}</ErrorBox>
              <p className="text-sm text-fg-muted">
                {t("confirmEmail.err_hint")}
              </p>
              <div className="flex gap-2">
                <Button
                  variant="secondary"
                  onClick={() => navigate("/login", { replace: true })}
                  className="flex-1"
                >
                  {t("confirmEmail.go_to_login")}
                </Button>
                <Link
                  to="/profile"
                  className="inline-flex flex-1 items-center justify-center rounded-md border border-border bg-panel px-3 py-1.5 text-sm font-medium text-fg hover:bg-panel-2"
                >
                  {t("confirmEmail.profile")}
                </Link>
              </div>
            </>
          )}
        </div>
      </Panel>
    </div>
  );
}
