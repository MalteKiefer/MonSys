import { Activity, KeyRound } from "lucide-react";
import { FormEvent, useState } from "react";
import { useNavigate } from "react-router-dom";

import { Button, ErrorBox, Field, Panel, TextInput } from "../components/ui";
import { api, ApiError } from "../lib/api";
import { useAuth } from "../lib/auth";
import { LoginResponse } from "../lib/types";

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
                <TextInput type="email" autoComplete="email" required value={email} onChange={(e) => setEmail(e.target.value)} />
              </Field>
              <Field label="Password">
                <TextInput type="password" autoComplete="current-password" required value={password} onChange={(e) => setPassword(e.target.value)} />
              </Field>
              {error && <ErrorBox>{error}</ErrorBox>}
              <Button type="submit" variant="primary" disabled={submitting} className="w-full">
                {submitting ? "Signing in…" : "Sign in"}
              </Button>
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
