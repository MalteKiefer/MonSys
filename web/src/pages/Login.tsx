import { Activity, Fingerprint, KeyRound } from "lucide-react";
import { FormEvent, useEffect, useRef, useState } from "react";
import { useNavigate } from "react-router-dom";

import { Button, ErrorBox, Field, Panel, TextInput } from "../components/ui";
import { api, ApiError } from "../lib/api";
import { useAuth } from "../lib/auth";
import { LoginResponse } from "../lib/types";
import {
  conditionalAutofill,
  loginWithPasskey,
  supported as webauthnSupported,
} from "../lib/webauthn";

type Stage = { kind: "password" } | { kind: "totp"; challengeToken: string };

export function Login() {
  const navigate = useNavigate();
  const setSession = useAuth((s) => s.setSession);
  const [email, setEmail] = useState("");
  const [password, setPassword] = useState("");
  const [code, setCode] = useState("");
  const [stage, setStage] = useState<Stage>({ kind: "password" });
  const [submitting, setSubmitting] = useState(false);
  const [error, setError] = useState<string | null>(null);

  // Conditional-mediation autofill: if the browser supports WebAuthn, kick off
  // a passive `get()` call tied to the email field's `autocomplete="username
  // webauthn"`. The browser will surface saved passkeys alongside username
  // suggestions; the abort controller cancels the call when the component
  // unmounts (or when the explicit passkey button is clicked).
  const abortRef = useRef<AbortController | null>(null);
  useEffect(() => {
    if (!webauthnSupported()) return;
    const ctrl = new AbortController();
    abortRef.current = ctrl;
    (async () => {
      const resp = await conditionalAutofill(ctrl.signal);
      if (!resp?.token) return;
      setSession(resp.token, resp.user);
      navigate("/", { replace: true });
    })();
    return () => ctrl.abort();
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, []);

  async function handlePassword(e: FormEvent) {
    e.preventDefault();
    setError(null);
    setSubmitting(true);
    try {
      const resp = await api<LoginResponse>("/v1/auth/login", {
        method: "POST",
        skipAuth: true,
        body: JSON.stringify({ email, password }),
      });
      if (resp.needs_passkey) {
        setError(
          "Administrator requires a passkey. Please sign in with your passkey.",
        );
        return;
      }
      if (resp.needs_totp && resp.challenge_token) {
        setStage({ kind: "totp", challengeToken: resp.challenge_token });
        return;
      }
      if (!resp.token) throw new Error("server did not return a session");
      setSession(resp.token, resp.user);
      navigate("/", { replace: true });
    } catch (err) {
      setError(err instanceof ApiError ? err.detail : "network error");
    } finally {
      setSubmitting(false);
    }
  }

  async function handleTOTP(e: FormEvent) {
    e.preventDefault();
    if (stage.kind !== "totp") return;
    setError(null);
    setSubmitting(true);
    try {
      const resp = await api<LoginResponse>("/v1/auth/2fa/challenge", {
        method: "POST",
        skipAuth: true,
        body: JSON.stringify({ challenge_token: stage.challengeToken, code }),
      });
      if (!resp.token) throw new Error("missing token");
      setSession(resp.token, resp.user);
      navigate("/", { replace: true });
    } catch (err) {
      setError(err instanceof ApiError ? err.detail : "network error");
    } finally {
      setSubmitting(false);
    }
  }

  return (
    <div className="flex min-h-full items-center justify-center px-4 py-10">
      <Panel className="w-full max-w-sm">
        <div className="space-y-5 p-6">
          <div className="flex items-center gap-2">
            {stage.kind === "password" ? (
              <Activity className="h-5 w-5 text-accent" />
            ) : (
              <KeyRound className="h-5 w-5 text-accent" />
            )}
            <h1 className="text-lg font-semibold">
              {stage.kind === "password" ? "Sign in" : "Two-factor"}
            </h1>
          </div>

          {stage.kind === "password" ? (
            <form onSubmit={handlePassword} className="space-y-4">
              <Field label="Email">
                <TextInput type="email" autoComplete="username webauthn" required value={email} onChange={(e) => setEmail(e.target.value)} />
              </Field>
              <Field label="Password">
                <TextInput type="password" autoComplete="current-password" required value={password} onChange={(e) => setPassword(e.target.value)} />
              </Field>
              {error && <ErrorBox>{error}</ErrorBox>}
              <Button type="submit" variant="primary" disabled={submitting} className="w-full">
                {submitting ? "Signing in…" : "Sign in"}
              </Button>
              {webauthnSupported() && (
                <>
                  <div className="my-3 flex items-center gap-2 text-xs text-fg-muted">
                    <span className="h-px flex-1 bg-border" />
                    <span>or</span>
                    <span className="h-px flex-1 bg-border" />
                  </div>
                  <Button
                    type="button"
                    variant="secondary"
                    disabled={submitting}
                    onClick={async () => {
                      setError(null);
                      setSubmitting(true);
                      try {
                        // Aborting the conditional-mediation call avoids the
                        // "InvalidStateError — operation already in flight"
                        // some browsers raise when both calls are pending.
                        abortRef.current?.abort();
                        const resp = await loginWithPasskey();
                        if (!resp.token) throw new Error("server did not return a session");
                        setSession(resp.token, resp.user);
                        navigate("/", { replace: true });
                      } catch (err: any) {
                        if (err?.name === "NotAllowedError") {
                          // User cancelled — silent.
                        } else {
                          setError(err?.message ?? "passkey login failed");
                        }
                      } finally {
                        setSubmitting(false);
                      }
                    }}
                    className="w-full"
                  >
                    <Fingerprint className="mr-2 inline h-4 w-4" />
                    Mit Passkey anmelden
                  </Button>
                </>
              )}
            </form>
          ) : (
            <form onSubmit={handleTOTP} className="space-y-4">
              <p className="text-sm text-fg-muted">
                Enter the 6-digit code from your authenticator (or a backup code).
              </p>
              <Field label="Code">
                <TextInput
                  type="text"
                  autoComplete="one-time-code"
                  required
                  value={code}
                  onChange={(e) => setCode(e.target.value)}
                  className="font-mono tracking-widest"
                />
              </Field>
              {error && <ErrorBox>{error}</ErrorBox>}
              <Button type="submit" variant="primary" disabled={submitting} className="w-full">
                {submitting ? "Verifying…" : "Verify"}
              </Button>
              <button
                type="button"
                onClick={() => {
                  setStage({ kind: "password" });
                  setCode("");
                  setError(null);
                }}
                className="block w-full text-center text-xs text-fg-muted hover:text-fg"
              >
                Cancel
              </button>
            </form>
          )}
        </div>
      </Panel>
    </div>
  );
}
