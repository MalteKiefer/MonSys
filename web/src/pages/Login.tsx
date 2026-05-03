import { FormEvent, useState } from "react";
import { useNavigate } from "react-router-dom";

import { api, ApiError } from "../lib/api";
import { useAuth } from "../lib/auth";
import { LoginResponse } from "../lib/types";

// Two-step login: password → optional TOTP challenge → session.
// Both steps share this component so we don't dance state across pages
// during what is logically one flow.
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
      if (!resp.token) {
        throw new Error("server did not return a session");
      }
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
        body: JSON.stringify({
          challenge_token: stage.challengeToken,
          code,
        }),
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
    <div className="flex min-h-full items-center justify-center px-4">
      {stage.kind === "password" ? (
        <form
          onSubmit={handlePassword}
          className="w-full max-w-sm space-y-4 rounded-lg border border-zinc-800 bg-zinc-900 p-6"
        >
          <h1 className="text-xl font-semibold">mon</h1>
          <p className="text-sm text-zinc-400">Sign in to continue.</p>

          <Field
            label="Email"
            type="email"
            value={email}
            autoComplete="email"
            onChange={setEmail}
          />
          <Field
            label="Password"
            type="password"
            value={password}
            autoComplete="current-password"
            onChange={setPassword}
          />

          {error && <ErrorBox text={error} />}

          <SubmitButton submitting={submitting} idle="Sign in" busy="Signing in…" />
        </form>
      ) : (
        <form
          onSubmit={handleTOTP}
          className="w-full max-w-sm space-y-4 rounded-lg border border-zinc-800 bg-zinc-900 p-6"
        >
          <h1 className="text-xl font-semibold">2FA</h1>
          <p className="text-sm text-zinc-400">
            Enter the 6-digit code from your authenticator (or a backup code).
          </p>

          <Field
            label="Code"
            type="text"
            value={code}
            autoComplete="one-time-code"
            onChange={setCode}
            inputClassName="font-mono tracking-widest"
          />

          {error && <ErrorBox text={error} />}

          <SubmitButton submitting={submitting} idle="Verify" busy="Verifying…" />

          <button
            type="button"
            onClick={() => {
              setStage({ kind: "password" });
              setCode("");
              setError(null);
            }}
            className="block w-full text-center text-xs text-zinc-400 hover:text-zinc-200"
          >
            Cancel
          </button>
        </form>
      )}
    </div>
  );
}

function Field(props: {
  label: string;
  type: string;
  value: string;
  autoComplete?: string;
  inputClassName?: string;
  onChange: (v: string) => void;
}) {
  return (
    <label className="block">
      <span className="text-sm text-zinc-300">{props.label}</span>
      <input
        type={props.type}
        required
        value={props.value}
        autoComplete={props.autoComplete}
        onChange={(e) => props.onChange(e.target.value)}
        className={`mt-1 w-full rounded border border-zinc-700 bg-zinc-950 px-3 py-2 text-sm focus:border-zinc-500 focus:outline-none ${props.inputClassName ?? ""}`}
      />
    </label>
  );
}

function ErrorBox({ text }: { text: string }) {
  return (
    <p className="rounded border border-fail/50 bg-fail/10 px-3 py-2 text-sm text-fail">
      {text}
    </p>
  );
}

function SubmitButton({ submitting, idle, busy }: { submitting: boolean; idle: string; busy: string }) {
  return (
    <button
      type="submit"
      disabled={submitting}
      className="w-full rounded bg-zinc-100 py-2 text-sm font-medium text-zinc-900 hover:bg-white disabled:opacity-50"
    >
      {submitting ? busy : idle}
    </button>
  );
}
