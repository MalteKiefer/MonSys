// Self-enrollment modal. Two states: a form that POSTs to
// /v1/admin/agents/enrollments to mint a one-shot token, then a result view
// with the install command + a live "waiting for first check-in…" poller.
// The poller stops as soon as the server records `used_at` on the enrollment
// (i.e. the agent successfully claimed the token).

import { useMutation, useQuery } from "@tanstack/react-query";
import {
  AlertCircle,
  Check,
  CheckCircle2,
  ChevronDown,
  ChevronUp,
  Code2,
  Copy,
  Download,
  Loader,
  QrCode,
  X,
} from "lucide-react";
import type { SyntheticEvent} from "react";
import { useEffect, useMemo, useState } from "react";
import { useNavigate } from "react-router-dom";

import { api, ApiError } from "../lib/api";
import type {
  AgentEnrollment,
  AgentEnrollmentCreateResponse,
  AgentEnrollmentInput,
  HostGroup,
} from "../lib/types";
import { Button, ErrorBox, Field, TextInput } from "./ui";

// ---- Modal shell ----------------------------------------------------------

function ModalShell({
  title,
  onClose,
  children,
}: {
  title: string;
  onClose: () => void;
  children: React.ReactNode;
}) {
  // Lock body scroll while open + close on Escape.
  useEffect(() => {
    const prev = document.body.style.overflow;
    document.body.style.overflow = "hidden";
    function onKey(e: KeyboardEvent) {
      if (e.key === "Escape") onClose();
    }
    window.addEventListener("keydown", onKey);
    return () => {
      document.body.style.overflow = prev;
      window.removeEventListener("keydown", onKey);
    };
  }, [onClose]);

  return (
    <div
      role="dialog"
      aria-modal="true"
      aria-label={title}
      className="fixed inset-0 z-50 flex items-start justify-center overflow-y-auto bg-bg/70 px-4 py-10 backdrop-blur-sm"
      onMouseDown={(e) => {
        if (e.target === e.currentTarget) onClose();
      }}
    >
      <section className="w-full max-w-2xl rounded-lg border border-border bg-panel shadow-panel-strong">
        <header className="flex items-center justify-between border-b border-border px-5 py-3">
          <h3 className="text-sm font-semibold">{title}</h3>
          <button
            type="button"
            onClick={onClose}
            aria-label="Close"
            className="rounded-md p-1 text-fg-muted transition-colors hover:bg-panel-2 hover:text-fg"
          >
            <X className="h-4 w-4" />
          </button>
        </header>
        <div className="p-5">{children}</div>
      </section>
    </div>
  );
}

// ---- Top-level modal ------------------------------------------------------

export function EnrollAgentModal({ onClose }: { onClose: () => void }) {
  const [created, setCreated] = useState<AgentEnrollmentCreateResponse | null>(null);

  return (
    <ModalShell title="Add agent" onClose={onClose}>
      {created ? (
        <ResultView created={created} onClose={onClose} />
      ) : (
        <FormView onCreated={setCreated} onCancel={onClose} />
      )}
    </ModalShell>
  );
}

// ---- State 1: form --------------------------------------------------------

function FormView({
  onCreated,
  onCancel,
}: {
  onCreated: (r: AgentEnrollmentCreateResponse) => void;
  onCancel: () => void;
}) {
  const tagsQuery = useQuery({
    queryKey: ["tags"],
    queryFn: () => api<{ tags: { tag: string; count: number }[] }>("/v1/tags"),
  });
  const groupsQuery = useQuery({
    queryKey: ["groups"],
    queryFn: () => api<{ groups: HostGroup[] }>("/v1/groups"),
  });

  const [label, setLabel] = useState("");
  const [description, setDescription] = useState("");
  const [tagsRaw, setTagsRaw] = useState("");
  const [groupIDs, setGroupIDs] = useState<string[]>([]);
  const [ttlMinutes, setTTLMinutes] = useState(30);
  const [error, setError] = useState<string | null>(null);

  const create = useMutation({
    mutationFn: () => {
      if (ttlMinutes < 5 || ttlMinutes > 1440) {
        throw new Error("TTL must be between 5 and 1440 minutes.");
      }
      const tags = tagsRaw
        .split(",")
        .map((s) => s.trim().toLowerCase())
        .filter(Boolean);
      const body: AgentEnrollmentInput = {
        label: label.trim() || undefined,
        description: description.trim() || undefined,
        tags: tags.length ? tags : undefined,
        group_ids: groupIDs.length ? groupIDs : undefined,
        ttl_minutes: ttlMinutes,
      };
      return api<AgentEnrollmentCreateResponse>("/v1/admin/agents/enrollments", {
        method: "POST",
        body: JSON.stringify(body),
      });
    },
    onSuccess: onCreated,
    onError: (err) => { setError(err instanceof ApiError ? err.detail : (err).message); },
  });

  function submit(e: SyntheticEvent) {
    e.preventDefault();
    setError(null);
    create.mutate();
  }

  return (
    <form onSubmit={submit} className="space-y-4">
      <p className="text-xs text-fg-subtle">
        Generates a single-use enrollment token. The new agent claims it on its first check-in
        and inherits the label, tags, and groups you set here.
      </p>

      <div className="grid grid-cols-2 gap-3">
        <Field label="Display label" hint="Optional. Shown in the host list before the first hostname is reported.">
          <TextInput
            value={label}
            onChange={(e) => { setLabel(e.target.value); }}
            placeholder="e.g. db-replica-3"
            maxLength={120}
          />
        </Field>
        <Field label="Token TTL (minutes)" hint="Min 5, max 1440 (24h). Default 30.">
          <TextInput
            type="number"
            min={5}
            max={1440}
            value={ttlMinutes}
            onChange={(e) => { setTTLMinutes(parseInt(e.target.value || "0", 10)); }}
          />
        </Field>
      </div>

      <Field label="Description" hint={`Optional, max 200 chars. (${description.length}/200)`}>
        <TextInput
          value={description}
          onChange={(e) => { setDescription(e.target.value.slice(0, 200)); }}
          placeholder="Why this host is being added"
          maxLength={200}
        />
      </Field>

      <Field
        label="Tags (comma-separated)"
        hint={
          tagsQuery.data?.tags?.length
            ? `Existing: ${tagsQuery.data.tags
                .slice(0, 12)
                .map((t) => t.tag)
                .join(", ")}`
            : "No tags defined yet."
        }
      >
        <TextInput
          value={tagsRaw}
          onChange={(e) => { setTagsRaw(e.target.value); }}
          placeholder="prod, db"
          className="font-mono"
        />
      </Field>

      <Field label="Groups (Ctrl/⌘ to multi-select)">
        <select
          multiple
          size={Math.min(5, Math.max(2, groupsQuery.data?.groups.length ?? 2))}
          value={groupIDs}
          onChange={(e) =>
            { setGroupIDs(Array.from(e.target.selectedOptions).map((o) => o.value)); }
          }
          className="w-full rounded-md border border-border bg-panel px-3 py-2 text-sm focus:border-accent focus:outline-none focus:ring-2 focus:ring-accent/30"
        >
          {(groupsQuery.data?.groups ?? []).map((g) => (
            <option key={g.id} value={g.id}>
              {g.name} ({g.member_ids.length})
            </option>
          ))}
        </select>
      </Field>

      {error && <ErrorBox>{error}</ErrorBox>}

      <div className="flex items-center justify-end gap-2 pt-2">
        <Button type="button" onClick={onCancel}>
          Cancel
        </Button>
        <Button variant="primary" type="submit" disabled={create.isPending}>
          {create.isPending ? "Generating…" : "Generate install command"}
        </Button>
      </div>
    </form>
  );
}

// ---- State 2: result + poller --------------------------------------------

function ResultView({
  created,
  onClose,
}: {
  created: AgentEnrollmentCreateResponse;
  onClose: () => void;
}) {
  const navigate = useNavigate();
  const enrollmentID = created.enrollment.id;

  // Poll until the agent claims the token (used_at != null) or it expires.
  // refetchInterval returns false to stop polling; the query also self-stops
  // once the local data already contains used_at.
  const poll = useQuery({
    queryKey: ["agent-enrollment", enrollmentID],
    queryFn: () =>
      api<{ enrollment: AgentEnrollment }>(`/v1/admin/agents/enrollments/${enrollmentID}`),
    refetchInterval: (q) => {
      const data = q.state.data;
      if (data?.enrollment.used_at) return false;
      // Stop polling once the token's TTL has elapsed; the agent can no
      // longer claim it server-side, so further requests are noise.
      const exp = data ? new Date(data.enrollment.expires_at).getTime() : 0;
      if (exp && exp <= Date.now()) return false;
      return 2000;
    },
    // Seed cache so the UI doesn't flicker before the first poll lands.
    initialData: { enrollment: created.enrollment },
  });

  const enrollment = poll.data?.enrollment ?? created.enrollment;
  const connected = !!enrollment.used_at;

  const [copiedCmd, setCopiedCmd] = useState(false);
  const [copiedURL, setCopiedURL] = useState(false);
  const [showScript, setShowScript] = useState(false);
  const [showQR, setShowQR] = useState(false);

  async function copyText(text: string, setFlag: (b: boolean) => void) {
    try {
      await navigator.clipboard.writeText(text);
      setFlag(true);
      setTimeout(() => { setFlag(false); }, 1500);
    } catch {
      /* clipboard may be unavailable on insecure origins; the user can select manually */
    }
  }

  // Recompute the relative-expiry string each tick so it stays fresh while
  // the modal is open. Cheap timer; pauses naturally when modal unmounts.
  // We also re-derive `expired` from this tick so the StatusRow flips to
  // the expired-before-registered state without waiting on a poll cycle.
  // Date.now() seed is intentional — `now` is a clock that ticks via setInterval
  // below; the rule's clock-hook suggestion doesn't apply for an interval-driven
  // mount-only value.
  // eslint-disable-next-line react-hooks/purity
  const [now, setNow] = useState(Date.now());
  useEffect(() => {
    if (connected) return;
    const t = setInterval(() => { setNow(Date.now()); }, 1000);
    return () => { clearInterval(t); };
  }, [connected]);

  const expiresIn = useMemo(
    () => relativeFuture(enrollment.expires_at, now),
    [enrollment.expires_at, now],
  );
  const expired = !connected && new Date(enrollment.expires_at).getTime() <= now;
  const tokenPrefix = created.token.slice(0, 8);

  return (
    <div className="space-y-4">
      <p className="text-xs text-fg-subtle">
        Run the command below on the new host as root. The token is single-use and expires{" "}
        <span className="text-fg-muted">{expiresIn}</span>.
      </p>

      <div className="rounded-md border border-border bg-bg/60">
        <div className="flex items-start justify-between gap-3 px-3 py-2">
          <pre className="m-0 flex-1 whitespace-pre-wrap break-all font-mono text-xs text-fg">
            {created.install_command}
          </pre>
          <div className="flex shrink-0 flex-col gap-1.5 sm:flex-row">
            <Button
              variant="primary"
              onClick={() => { void copyText(created.install_command, setCopiedCmd); }}
              aria-label="Copy install command"
            >
              {copiedCmd ? (
                <>
                  <Check className="h-3.5 w-3.5" /> Copied
                </>
              ) : (
                <>
                  <Copy className="h-3.5 w-3.5" /> Copy command
                </>
              )}
            </Button>
            <Button
              onClick={() => { void copyText(created.install_url, setCopiedURL); }}
              aria-label="Copy install URL"
            >
              {copiedURL ? (
                <>
                  <Check className="h-3.5 w-3.5" /> Copied
                </>
              ) : (
                <>
                  <Copy className="h-3.5 w-3.5" /> Copy URL
                </>
              )}
            </Button>
          </div>
        </div>
      </div>

      <div className="space-y-2">
        <div className="flex flex-wrap items-center gap-4">
          <button
            type="button"
            onClick={() => { setShowScript((v) => !v); }}
            aria-expanded={showScript}
            className="inline-flex items-center gap-1.5 text-xs text-fg-muted transition-colors hover:text-fg"
          >
            <Code2 className="h-3.5 w-3.5" />
            View install script
            {showScript ? (
              <ChevronUp className="h-3.5 w-3.5" />
            ) : (
              <ChevronDown className="h-3.5 w-3.5" />
            )}
          </button>
          <button
            type="button"
            onClick={() => { setShowQR((v) => !v); }}
            aria-expanded={showQR}
            className="inline-flex items-center gap-1.5 text-xs text-fg-muted transition-colors hover:text-fg"
          >
            <QrCode className="h-3.5 w-3.5" />
            {showQR ? "Hide QR code" : "Show QR code"}
            {showQR ? (
              <ChevronUp className="h-3.5 w-3.5" />
            ) : (
              <ChevronDown className="h-3.5 w-3.5" />
            )}
          </button>
        </div>
        {showScript && (
          <InstallScriptViewer url={created.install_url} tokenPrefix={tokenPrefix} />
        )}
        {showQR && <InstallQR url={created.install_url} />}
      </div>

      <StatusRow
        connected={connected}
        expired={expired}
        hostname={enrollment.used_by_hostname}
        expiresIn={expiresIn}
        onOpenHost={() => {
          if (enrollment.used_by_host_id) {
            void navigate(`/hosts/${enrollment.used_by_host_id}`);
          }
        }}
      />

      <div className="flex items-center justify-end gap-2 pt-2">
        <Button variant="primary" onClick={onClose}>
          Close
        </Button>
      </div>
    </div>
  );
}

function StatusRow({
  connected,
  expired,
  hostname,
  expiresIn,
  onOpenHost,
}: {
  connected: boolean;
  expired: boolean;
  hostname?: string;
  expiresIn: string;
  onOpenHost: () => void;
}) {
  if (connected) {
    return (
      <div className="flex items-center justify-between rounded-md border border-ok/30 bg-ok/10 px-3 py-2 text-sm text-ok">
        <span className="inline-flex items-center gap-2">
          <CheckCircle2 className="h-4 w-4" />
          Connected as <span className="font-mono">{hostname || "unknown"}</span>
        </span>
        <Button onClick={onOpenHost}>Open host</Button>
      </div>
    );
  }
  if (expired) {
    return (
      <div className="inline-flex items-center gap-2 rounded-md border border-fail/30 bg-fail/10 px-3 py-2 text-sm text-fail">
        <AlertCircle className="h-4 w-4" />
        Token expired before agent registered. Close and try again.
      </div>
    );
  }
  return (
    <div className="inline-flex items-center gap-2 rounded-md border border-border bg-panel-2/60 px-3 py-2 text-sm text-fg-muted">
      <Loader className="h-4 w-4 animate-spin" />
      Waiting for first check-in… (token expires {expiresIn})
    </div>
  );
}

// ---- Install script viewer ------------------------------------------------

// Fetches the bootstrap script directly from the public install endpoint and
// renders it with download support. Uses fetch() rather than the api() helper
// because the response is text/x-shellscript (not JSON) and the endpoint is
// intentionally session-less.
function InstallScriptViewer({
  url,
  tokenPrefix,
}: {
  url: string;
  tokenPrefix: string;
}) {
  const [script, setScript] = useState<string | null>(null);
  const [error, setError] = useState<string | null>(null);
  const [loading, setLoading] = useState(false);
  const [attempt, setAttempt] = useState(0);

  useEffect(() => {
    let cancelled = false;
    setLoading(true);
    setError(null);
    fetch(url, { credentials: "omit" })
      .then(async (r) => {
        if (!r.ok) throw new Error(`HTTP ${r.status}`);
        return r.text();
      })
      .then((text) => {
        if (!cancelled) setScript(text);
      })
      .catch((e) => {
        if (!cancelled) setError((e as Error).message || "fetch failed");
      })
      .finally(() => {
        if (!cancelled) setLoading(false);
      });
    return () => {
      cancelled = true;
    };
  }, [url, attempt]);

  function download() {
    if (!script) return;
    const blob = new Blob([script], { type: "text/x-shellscript" });
    const href = URL.createObjectURL(blob);
    const a = document.createElement("a");
    a.href = href;
    a.download = `mon-agent-install-${tokenPrefix}.sh`;
    document.body.appendChild(a);
    a.click();
    a.remove();
    URL.revokeObjectURL(href);
  }

  if (loading) {
    return (
      <div className="mt-2 inline-flex items-center gap-2 rounded-md border border-border bg-bg/60 px-3 py-2 text-xs text-fg-muted">
        <Loader className="h-3.5 w-3.5 animate-spin" />
        Fetching script…
      </div>
    );
  }
  if (error) {
    return (
      <div className="mt-2 flex items-center justify-between gap-3 rounded-md border border-fail/30 bg-fail/10 px-3 py-2 text-xs text-fail">
        <span className="inline-flex items-center gap-2">
          <AlertCircle className="h-3.5 w-3.5" />
          Failed to load script
        </span>
        <Button onClick={() => { setAttempt((n) => n + 1); }} aria-label="Retry fetching script">
          Retry
        </Button>
      </div>
    );
  }
  return (
    <div className="mt-2 space-y-2">
      <div className="flex items-center justify-end">
        <Button onClick={download} disabled={!script} aria-label="Download install script">
          <Download className="h-3.5 w-3.5" /> Download .sh
        </Button>
      </div>
      <pre className="m-0 max-h-[400px] overflow-auto rounded-md border border-border bg-bg/60 px-3 py-2 font-mono text-[11px] text-fg-muted">
        {script}
      </pre>
    </div>
  );
}

// InstallQR shows the install URL as a scannable PNG, rendered server-side
// so we don't ship a QR encoder in the bundle. Falls back silently if the
// image fails to load (offline operator, blocked endpoint).
function InstallQR({ url }: { url: string }) {
  // The server endpoint /v1/agents/install-qr accepts ?t=<token>, so we
  // re-extract the token from the URL the modal already holds.
  const qrSrc = (() => {
    try {
      const u = new URL(url);
      const t = u.searchParams.get("t") ?? "";
      const base = `${u.protocol}//${u.host}`;
      return `${base}/v1/agents/install-qr?t=${encodeURIComponent(t)}`;
    } catch {
      return "";
    }
  })();
  if (!qrSrc) return null;
  return (
    <div className="mt-2 flex flex-col items-center gap-2 rounded-md border border-border bg-bg/40 p-4">
      <img
        src={qrSrc}
        alt="Scan with phone to open install URL"
        width={224}
        height={224}
        className="rounded-sm bg-white p-2"
        loading="lazy"
      />
      <span className="text-xs text-fg-subtle">Scan with a phone to open the install URL.</span>
    </div>
  );
}

// ---- Helpers --------------------------------------------------------------

function relativeFuture(iso: string, now: number = Date.now()): string {
  const t = new Date(iso).getTime();
  if (Number.isNaN(t)) return iso;
  const diff = (t - now) / 1000;
  if (diff <= 0) return "expired";
  if (diff < 60) return `in ${Math.round(diff)}s`;
  if (diff < 3600) return `in ${Math.round(diff / 60)}m`;
  if (diff < 86400) return `in ${Math.round(diff / 3600)}h`;
  return `in ${Math.round(diff / 86400)}d`;
}
