import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { KeyRound, Pencil, Smartphone, Trash2, Upload, User } from "lucide-react";
import { ChangeEvent, FormEvent, ReactNode, useRef, useState } from "react";
import { Trans } from "react-i18next";
import { useSearchParams } from "react-router-dom";

import {
  Avatar,
  Button,
  ErrorBox,
  Field,
  Panel,
  PanelBody,
  PanelHeader,
  Skeleton,
  SuccessBox,
  TabItem,
  Tabs,
  TextInput,
} from "../components/ui";
import { useT } from "../i18n/useT";
import { api, ApiError } from "../lib/api";
import { DensityProvider, useDensityStore, type Density } from "../lib/density-store";
import { CurrentUser, ListPasskeysResponse, Passkey, TOTPSetup } from "../lib/types";
import { registerPasskey, supported as webauthnSupported } from "../lib/webauthn";

type Msg = { kind: "ok" | "err"; text: string } | null;

type ProfileTab = "account" | "two_factor" | "passkeys";

const TAB_KEYS: ReadonlyArray<ProfileTab> = ["account", "two_factor", "passkeys"];

function parseTab(raw: string | null): ProfileTab {
  return (TAB_KEYS as readonly string[]).includes(raw ?? "") ? (raw as ProfileTab) : "account";
}

export function Profile() {
  const { t } = useT(["profile", "common"]);
  const qc = useQueryClient();
  const [searchParams, setSearchParams] = useSearchParams();
  const tab = parseTab(searchParams.get("tab"));
  const setTab = (next: ProfileTab) => {
    const sp = new URLSearchParams(searchParams);
    sp.set("tab", next);
    setSearchParams(sp, { replace: true });
  };

  const me = useQuery({
    queryKey: ["me"],
    queryFn: () => api<CurrentUser>("/v1/auth/me"),
  });

  if (me.isLoading)
    return (
      <div className="mx-auto max-w-3xl space-y-4 p-6">
        <Skeleton className="h-8 w-48" />
        <Skeleton className="h-32" />
        <Skeleton className="h-32" />
        <Skeleton className="h-48" />
      </div>
    );
  if (me.error) return <p className="p-6 text-sm text-fail">{(me.error as Error).message}</p>;
  const user = me.data!;

  const items: ReadonlyArray<TabItem<ProfileTab>> = [
    { key: "account", label: t("profile:tabs.account"), icon: User },
    { key: "two_factor", label: t("profile:tabs.twoFactor"), icon: Smartphone },
    { key: "passkeys", label: t("profile:tabs.passkeys"), icon: KeyRound },
  ];

  return (
    <div className="mx-auto max-w-3xl p-6">
      {/* Mount the html[data-density] side effect from this page. The
          provider is a no-op render — it just mirrors the persisted store
          value onto <html>. Remove once App.tsx (Phase A) hosts it. */}
      <DensityProvider />
      <header className="mb-4">
        <p className="text-sm text-fg-muted">
          <Trans
            ns="profile"
            i18nKey="signedInAs"
            values={{ email: user.email, role: user.role }}
            components={{ 1: <span className="text-fg" /> }}
          />
        </p>
      </header>

      <Tabs<ProfileTab> items={items} value={tab} onChange={setTab} />

      <div
        id={`panel-${tab}`}
        role="tabpanel"
        aria-labelledby={`tab-${tab}`}
        className="mt-6 space-y-6"
      >
        {tab === "account" && (
          <>
            <AvatarCard user={user} />
            <ChangeEmailCard />
            <ChangePasswordCard />
            <DisplayCard />
          </>
        )}
        {tab === "two_factor" && (
          <TwoFactorCard
            active={user.totp_active}
            onSuccess={() => qc.invalidateQueries({ queryKey: ["me"] })}
          />
        )}
        {tab === "passkeys" && <PasskeysCard />}
      </div>
    </div>
  );
}

function DisplayCard() {
  const { t } = useT(["profile", "common"]);
  const density = useDensityStore((s) => s.density);
  const setDensity = useDensityStore((s) => s.setDensity);
  const options: { value: Density; label: string; hint: string }[] = [
    {
      value: "compact",
      label: t("profile:display.compact.label"),
      hint: t("profile:display.compact.hint"),
    },
    {
      value: "comfortable",
      label: t("profile:display.comfortable.label"),
      hint: t("profile:display.comfortable.hint"),
    },
  ];
  return (
    <ProfilePanel title={t("profile:display.title")}>
      <div className="space-y-3">
        <p className="text-sm text-fg-muted">{t("profile:display.description")}</p>
        <div role="radiogroup" aria-label={t("profile:display.ariaLabel")} className="inline-flex rounded-md border border-border bg-panel p-0.5">
          {options.map((opt) => {
            const active = opt.value === density;
            return (
              <button
                key={opt.value}
                type="button"
                role="radio"
                aria-checked={active}
                onClick={() => setDensity(opt.value)}
                className={`min-h-9 rounded px-3 py-1.5 text-sm font-medium transition-colors duration-150 ${
                  active ? "bg-panel-2 text-fg shadow-panel" : "text-fg-muted hover:text-fg"
                }`}
              >
                {opt.label}
              </button>
            );
          })}
        </div>
        <p className="text-xs text-fg-subtle">
          {options.find((o) => o.value === density)?.hint}
        </p>
      </div>
    </ProfilePanel>
  );
}

function ProfilePanel({ title, children }: { title: string; children: ReactNode }) {
  return (
    <Panel>
      <PanelHeader>
        <h3 className="text-sm font-semibold text-fg">{title}</h3>
      </PanelHeader>
      <PanelBody>{children}</PanelBody>
    </Panel>
  );
}

// AvatarCard — upload / remove the user's avatar. We resize+encode on the
// client to keep the wire payload small (≤ 512 KiB decoded is the server
// limit) and to avoid a server-side image lib. Anything bigger than 5 MiB
// pre-resize is rejected up front with a friendly message.
const AVATAR_MAX_INPUT_BYTES = 5 * 1024 * 1024; // 5 MiB
const AVATAR_TARGET_SIZE = 400; // px — square output, downscaled with cover-crop

async function fileToCroppedWebp(
  file: File,
  messages: {
    decodeFailed: string;
    canvasUnavailable: string;
    encodingFailed: string;
  },
): Promise<{ blob: Blob; b64: string }> {
  // Decode the image off the main thread when supported, fall back to an
  // <img> element otherwise. We don't bother with createImageBitmap on
  // unsupported browsers — the fallback path still runs in tens of ms for
  // typical avatar inputs.
  const url = URL.createObjectURL(file);
  try {
    const img = await new Promise<HTMLImageElement>((resolve, reject) => {
      const el = new Image();
      el.onload = () => resolve(el);
      el.onerror = () => reject(new Error(messages.decodeFailed));
      el.src = url;
    });

    const side = Math.min(img.naturalWidth, img.naturalHeight);
    const sx = Math.max(0, Math.floor((img.naturalWidth - side) / 2));
    const sy = Math.max(0, Math.floor((img.naturalHeight - side) / 2));

    const canvas = document.createElement("canvas");
    canvas.width = AVATAR_TARGET_SIZE;
    canvas.height = AVATAR_TARGET_SIZE;
    const ctx = canvas.getContext("2d");
    if (!ctx) throw new Error(messages.canvasUnavailable);
    ctx.drawImage(img, sx, sy, side, side, 0, 0, AVATAR_TARGET_SIZE, AVATAR_TARGET_SIZE);

    const blob: Blob = await new Promise((resolve, reject) => {
      canvas.toBlob(
        (b) => (b ? resolve(b) : reject(new Error(messages.encodingFailed))),
        "image/webp",
        0.9,
      );
    });
    const b64 = await blobToBase64(blob, {
      result: messages.encodingFailed,
      generic: messages.encodingFailed,
    });
    return { blob, b64 };
  } finally {
    URL.revokeObjectURL(url);
  }
}

function blobToBase64(
  blob: Blob,
  messages: { result: string; generic: string },
): Promise<string> {
  return new Promise((resolve, reject) => {
    const fr = new FileReader();
    fr.onload = () => {
      const result = fr.result;
      if (typeof result !== "string") {
        reject(new Error(messages.result));
        return;
      }
      // result is "data:image/webp;base64,XXXX" — strip the prefix.
      const idx = result.indexOf(",");
      resolve(idx >= 0 ? result.slice(idx + 1) : result);
    };
    fr.onerror = () => reject(fr.error ?? new Error(messages.generic));
    fr.readAsDataURL(blob);
  });
}

function AvatarCard({ user }: { user: CurrentUser }) {
  const { t } = useT(["profile", "common"]);
  const qc = useQueryClient();
  const inputRef = useRef<HTMLInputElement | null>(null);
  const [busy, setBusy] = useState(false);
  const [msg, setMsg] = useState<Msg>(null);

  async function onPick(e: ChangeEvent<HTMLInputElement>) {
    const file = e.target.files?.[0];
    // Clear the input value so the same file can be re-selected later
    // (browsers suppress the change event for identical pick-throughs).
    e.target.value = "";
    if (!file) return;
    setMsg(null);
    if (file.size > AVATAR_MAX_INPUT_BYTES) {
      setMsg({ kind: "err", text: t("profile:avatar.errors.tooLarge") });
      return;
    }
    setBusy(true);
    try {
      const { b64 } = await fileToCroppedWebp(file, {
        decodeFailed: t("profile:avatar.errors.decodeFailed"),
        canvasUnavailable: t("profile:avatar.errors.canvasUnavailable"),
        encodingFailed: t("profile:avatar.errors.encodingFailed"),
      });
      // Server limit is 512 KiB decoded; 400×400 webp@0.9 lands well under
      // that for realistic photos but we still bail loudly if it ever runs
      // away (e.g. extreme noise input).
      const decodedBytes = Math.floor((b64.length * 3) / 4);
      if (decodedBytes > 512 * 1024) {
        setMsg({ kind: "err", text: t("profile:avatar.errors.resizedTooLarge") });
        return;
      }
      await api<{ ok: boolean }>("/v1/auth/me/avatar", {
        method: "POST",
        body: JSON.stringify({ content_type: "image/webp", data_b64: b64 }),
      });
      setMsg({ kind: "ok", text: t("profile:avatar.success.updated") });
      qc.invalidateQueries({ queryKey: ["me"] });
    } catch (err) {
      setMsg({ kind: "err", text: err instanceof ApiError ? err.detail : (err as Error).message });
    } finally {
      setBusy(false);
    }
  }

  async function onRemove() {
    setMsg(null);
    setBusy(true);
    try {
      await api<{ ok: boolean }>("/v1/auth/me/avatar", { method: "DELETE" });
      setMsg({ kind: "ok", text: t("profile:avatar.success.removed") });
      qc.invalidateQueries({ queryKey: ["me"] });
    } catch (err) {
      setMsg({ kind: "err", text: err instanceof ApiError ? err.detail : (err as Error).message });
    } finally {
      setBusy(false);
    }
  }

  return (
    <ProfilePanel title={t("profile:avatar.title")}>
      <div className="flex flex-wrap items-start gap-5">
        <Avatar
          userId={user.id}
          hasAvatar={user.has_avatar}
          updatedAt={user.avatar_updated_at}
          email={user.email}
          size="lg"
        />
        <div className="flex-1 min-w-[12rem] space-y-2">
          <div className="flex flex-wrap items-center gap-2">
            <input
              ref={inputRef}
              type="file"
              accept="image/png,image/jpeg,image/webp"
              className="hidden"
              onChange={onPick}
            />
            <Button
              type="button"
              variant="primary"
              disabled={busy}
              onClick={() => inputRef.current?.click()}
            >
              <Upload className="h-3.5 w-3.5" />
              {busy
                ? t("profile:avatar.working")
                : user.has_avatar
                  ? t("profile:avatar.replace")
                  : t("profile:avatar.uploadNew")}
            </Button>
            {user.has_avatar && (
              <Button type="button" variant="danger" disabled={busy} onClick={onRemove}>
                <Trash2 className="h-3.5 w-3.5" />
                {t("profile:avatar.remove")}
              </Button>
            )}
          </div>
          <p className="text-xs text-fg-subtle">{t("profile:avatar.helpText")}</p>
          {msg && <Message msg={msg} />}
        </div>
      </div>
    </ProfilePanel>
  );
}

// ChangeEmailCard — two-step verified email change. Step 1 (this card)
// posts {new_email, current_password} to /v1/auth/me/email/request which
// mails a confirmation link to the NEW address. Step 2 happens on
// /confirm-email when the user clicks that link — that endpoint updates
// the email and revokes every session for the user. We never mutate the
// email in place from here.
function ChangeEmailCard() {
  const { t } = useT(["profile", "common"]);
  const [pw, setPw] = useState("");
  const [email, setEmail] = useState("");
  const [msg, setMsg] = useState<Msg>(null);
  const [busy, setBusy] = useState(false);
  const [pendingEmail, setPendingEmail] = useState<string | null>(null);

  async function submit(e: FormEvent) {
    e.preventDefault();
    setMsg(null);
    setBusy(true);
    try {
      await api<{ ok: boolean }>("/v1/auth/me/email/request", {
        method: "POST",
        body: JSON.stringify({ current_password: pw, new_email: email }),
      });
      setPendingEmail(email);
      setPw("");
      setEmail("");
    } catch (err) {
      setMsg({
        kind: "err",
        text: err instanceof ApiError ? err.detail : t("profile:email.genericFailure"),
      });
    } finally {
      setBusy(false);
    }
  }

  if (pendingEmail) {
    return (
      <ProfilePanel title={t("profile:email.title")}>
        <div className="space-y-3">
          <SuccessBox>
            <Trans
              ns="profile"
              i18nKey="email.pending.message"
              values={{ email: pendingEmail }}
              components={{ 1: <span className="font-medium" /> }}
            />
          </SuccessBox>
          <p className="text-xs text-fg-subtle">{t("profile:email.pending.hint")}</p>
          <Button type="button" variant="ghost" onClick={() => setPendingEmail(null)}>
            {t("profile:email.pending.sendAnother")}
          </Button>
        </div>
      </ProfilePanel>
    );
  }

  return (
    <ProfilePanel title={t("profile:email.title")}>
      <form onSubmit={submit} className="space-y-3">
        <p className="text-sm text-fg-muted">{t("profile:email.description")}</p>
        <Field label={t("profile:email.currentPassword")}>
          <TextInput
            type="password"
            required
            value={pw}
            onChange={(e: ChangeEvent<HTMLInputElement>) => setPw(e.target.value)}
          />
        </Field>
        <Field label={t("profile:email.newEmail")}>
          <TextInput
            type="email"
            required
            value={email}
            onChange={(e) => setEmail(e.target.value)}
          />
        </Field>
        <FormFooter
          busy={busy}
          idle={t("profile:email.submit")}
          busyLabel={t("profile:email.submitting")}
          msg={msg}
        />
      </form>
    </ProfilePanel>
  );
}

function ChangePasswordCard() {
  const { t } = useT(["profile", "common"]);
  const [cur, setCur] = useState("");
  const [next, setNext] = useState("");
  const [msg, setMsg] = useState<Msg>(null);
  const [busy, setBusy] = useState(false);

  async function submit(e: FormEvent) {
    e.preventDefault();
    setMsg(null);
    setBusy(true);
    try {
      await api<{ ok: boolean }>("/v1/auth/change-password", {
        method: "POST",
        body: JSON.stringify({ current_password: cur, new_password: next }),
      });
      setMsg({ kind: "ok", text: t("profile:password.success") });
      setCur("");
      setNext("");
    } catch (err) {
      setMsg({
        kind: "err",
        text: err instanceof ApiError ? err.detail : t("profile:password.genericFailure"),
      });
    } finally {
      setBusy(false);
    }
  }

  return (
    <ProfilePanel title={t("profile:password.title")}>
      <form onSubmit={submit} className="space-y-3">
        <Field label={t("profile:password.currentPassword")}>
          <TextInput type="password" required value={cur} onChange={(e) => setCur(e.target.value)} />
        </Field>
        <Field label={t("profile:password.newPassword")}>
          <TextInput type="password" required value={next} onChange={(e) => setNext(e.target.value)} />
        </Field>
        <FormFooter
          busy={busy}
          idle={t("profile:password.submit")}
          busyLabel={t("profile:password.submitting")}
          msg={msg}
        />
      </form>
    </ProfilePanel>
  );
}

function TwoFactorCard({ active, onSuccess }: { active: boolean; onSuccess: () => void }) {
  const { t } = useT(["profile", "common"]);
  const [setup, setSetup] = useState<TOTPSetup | null>(null);
  const [code, setCode] = useState("");
  const [pw, setPw] = useState("");
  const [msg, setMsg] = useState<Msg>(null);
  const [busy, setBusy] = useState(false);

  async function startSetup() {
    setMsg(null);
    setBusy(true);
    try {
      const s = await api<TOTPSetup>("/v1/auth/2fa/setup", { method: "POST" });
      setSetup(s);
    } catch (err) {
      setMsg({
        kind: "err",
        text: err instanceof ApiError ? err.detail : t("profile:twoFactor.genericFailure"),
      });
    } finally {
      setBusy(false);
    }
  }

  async function verify(e: FormEvent) {
    e.preventDefault();
    setMsg(null);
    setBusy(true);
    try {
      await api<{ ok: boolean }>("/v1/auth/2fa/verify", {
        method: "POST",
        body: JSON.stringify({ code }),
      });
      setMsg({ kind: "ok", text: t("profile:twoFactor.successEnabled") });
      setCode("");
      onSuccess();
    } catch (err) {
      setMsg({
        kind: "err",
        text: err instanceof ApiError ? err.detail : t("profile:twoFactor.genericFailure"),
      });
    } finally {
      setBusy(false);
    }
  }

  async function disable(e: FormEvent) {
    e.preventDefault();
    setMsg(null);
    setBusy(true);
    try {
      await api<{ ok: boolean }>("/v1/auth/2fa/disable", {
        method: "POST",
        body: JSON.stringify({ password: pw }),
      });
      setMsg({ kind: "ok", text: t("profile:twoFactor.successDisabled") });
      setPw("");
      setSetup(null);
      onSuccess();
    } catch (err) {
      setMsg({
        kind: "err",
        text: err instanceof ApiError ? err.detail : t("profile:twoFactor.genericFailure"),
      });
    } finally {
      setBusy(false);
    }
  }

  return (
    <ProfilePanel
      title={active ? t("profile:twoFactor.titleActive") : t("profile:twoFactor.titleInactive")}
    >
      {active ? (
        <form onSubmit={disable} className="space-y-3">
          <p className="text-sm text-fg-muted">{t("profile:twoFactor.activeText")}</p>
          <Field label={t("profile:twoFactor.passwordLabel")}>
            <TextInput type="password" required value={pw} onChange={(e) => setPw(e.target.value)} />
          </Field>
          <FormFooter
            busy={busy}
            idle={t("profile:twoFactor.disableSubmit")}
            busyLabel={t("profile:twoFactor.disableSubmitting")}
            msg={msg}
            variant="danger"
          />
        </form>
      ) : !setup ? (
        <div className="space-y-3">
          <p className="text-sm text-fg-muted">{t("profile:twoFactor.introText")}</p>
          <Button variant="primary" onClick={startSetup} disabled={busy}>
            {busy ? t("profile:twoFactor.generating") : t("profile:twoFactor.beginSetup")}
          </Button>
          {msg && <Message msg={msg} />}
        </div>
      ) : (
        <div className="space-y-4">
          <div className="grid gap-4 md:grid-cols-2">
            <div>
              <p className="mb-2 text-xs uppercase tracking-wider text-fg-muted">
                {t("profile:twoFactor.scanLabel")}
              </p>
              <img
                src={`data:image/png;base64,${setup.qr_png_base64}`}
                alt={t("profile:twoFactor.qrAlt")}
                className="rounded border border-border bg-white p-2"
              />
              <p className="mt-2 text-xs text-fg-subtle">
                {t("profile:twoFactor.manualSecret")}
                <code className="ml-1 select-all rounded bg-panel-2 px-1 py-0.5 font-mono text-xs text-fg">
                  {setup.secret_b32}
                </code>
              </p>
            </div>
            <div>
              <p className="mb-2 text-xs uppercase tracking-wider text-fg-muted">
                {t("profile:twoFactor.backupCodesLabel")}
              </p>
              <ul className="grid grid-cols-2 gap-1 rounded border border-border bg-bg p-3 font-mono text-xs text-fg">
                {setup.backup_codes.map((c) => (
                  <li key={c} className="select-all">
                    {c}
                  </li>
                ))}
              </ul>
            </div>
          </div>

          <form onSubmit={verify} className="space-y-3">
            <Field label={t("profile:twoFactor.confirmCodeLabel")}>
              <TextInput
                type="text"
                required
                value={code}
                onChange={(e) => setCode(e.target.value)}
                className="font-mono tracking-widest"
              />
            </Field>
            <FormFooter
              busy={busy}
              idle={t("profile:twoFactor.activateSubmit")}
              busyLabel={t("profile:twoFactor.activateSubmitting")}
              msg={msg}
            />
          </form>
        </div>
      )}
    </ProfilePanel>
  );
}

function PasskeysCard() {
  const { t } = useT(["profile", "common"]);
  const qc = useQueryClient();
  const [name, setName] = useState("");
  const [msg, setMsg] = useState<Msg>(null);

  const passkeys = useQuery({
    queryKey: ["passkeys"],
    queryFn: () => api<ListPasskeysResponse>("/v1/auth/me/passkeys"),
    enabled: webauthnSupported(),
  });

  const addPasskey = useMutation({
    mutationFn: async (n: string) => registerPasskey(n),
    onSuccess: () => {
      setName("");
      setMsg({ kind: "ok", text: t("profile:passkeys.addSuccess") });
      qc.invalidateQueries({ queryKey: ["passkeys"] });
      qc.invalidateQueries({ queryKey: ["me"] });
    },
    onError: (err) => {
      setMsg({ kind: "err", text: err instanceof ApiError ? err.detail : (err as Error).message });
    },
  });

  const renamePasskey = useMutation({
    mutationFn: ({ id, name: n }: { id: string; name: string }) =>
      api<{ ok: boolean }>(`/v1/auth/me/passkeys/${id}`, {
        method: "PUT",
        body: JSON.stringify({ name: n }),
      }),
    onSuccess: () => {
      setMsg(null);
      qc.invalidateQueries({ queryKey: ["passkeys"] });
    },
    onError: (err) => {
      setMsg({ kind: "err", text: err instanceof ApiError ? err.detail : (err as Error).message });
    },
  });

  const deletePasskey = useMutation({
    mutationFn: (id: string) =>
      api<{ ok: boolean }>(`/v1/auth/me/passkeys/${id}`, { method: "DELETE" }),
    onSuccess: () => {
      setMsg(null);
      qc.invalidateQueries({ queryKey: ["passkeys"] });
      qc.invalidateQueries({ queryKey: ["me"] });
    },
    onError: (err) => {
      setMsg({ kind: "err", text: err instanceof ApiError ? err.detail : (err as Error).message });
    },
  });

  async function submitAdd(e: FormEvent) {
    e.preventDefault();
    setMsg(null);
    const trimmed = name.trim();
    if (!trimmed) {
      setMsg({ kind: "err", text: t("profile:passkeys.addNameRequired") });
      return;
    }
    addPasskey.mutate(trimmed);
  }

  if (!webauthnSupported()) {
    return (
      <ProfilePanel title={t("profile:passkeys.title")}>
        <div className="space-y-2">
          <p className="text-sm text-fg-muted">{t("profile:passkeys.description")}</p>
          <p className="text-sm text-fg-subtle">{t("profile:passkeys.unsupported")}</p>
        </div>
      </ProfilePanel>
    );
  }

  const list = passkeys.data?.passkeys ?? [];

  return (
    <ProfilePanel title={t("profile:passkeys.title")}>
      <div className="space-y-4">
        <p className="text-sm text-fg-muted">{t("profile:passkeys.description")}</p>

        <form onSubmit={submitAdd} className="flex flex-wrap items-end gap-2">
          <div className="flex-1 min-w-[12rem]">
            <Field label={t("profile:passkeys.nameLabel")}>
              <TextInput
                type="text"
                placeholder={t("profile:passkeys.namePlaceholder")}
                value={name}
                onChange={(e: ChangeEvent<HTMLInputElement>) => setName(e.target.value)}
                disabled={addPasskey.isPending}
              />
            </Field>
          </div>
          <Button type="submit" variant="primary" disabled={addPasskey.isPending}>
            <KeyRound className="h-3.5 w-3.5" />
            {addPasskey.isPending
              ? t("profile:passkeys.addSubmitting")
              : t("profile:passkeys.addSubmit")}
          </Button>
        </form>

        {msg && <Message msg={msg} />}

        {passkeys.isLoading ? (
          <Skeleton className="h-16" />
        ) : passkeys.error ? (
          <ErrorBox>{(passkeys.error as Error).message}</ErrorBox>
        ) : list.length === 0 ? (
          <p className="text-sm text-fg-subtle">{t("profile:passkeys.empty")}</p>
        ) : (
          <ul className="divide-y divide-border rounded border border-border bg-bg">
            {list.map((pk) => (
              <PasskeyRow
                key={pk.id}
                passkey={pk}
                onRename={(newName) => renamePasskey.mutate({ id: pk.id, name: newName })}
                onDelete={() => deletePasskey.mutate(pk.id)}
                renaming={renamePasskey.isPending}
                deleting={deletePasskey.isPending}
              />
            ))}
          </ul>
        )}
      </div>
    </ProfilePanel>
  );
}

function PasskeyRow({
  passkey,
  onRename,
  onDelete,
  renaming,
  deleting,
}: {
  passkey: Passkey;
  onRename: (name: string) => void;
  onDelete: () => void;
  renaming: boolean;
  deleting: boolean;
}) {
  const { t } = useT(["profile", "common"]);
  const [editing, setEditing] = useState(false);
  const [draft, setDraft] = useState(passkey.name);

  function saveName(e: FormEvent) {
    e.preventDefault();
    const trimmed = draft.trim();
    if (!trimmed || trimmed === passkey.name) {
      setEditing(false);
      setDraft(passkey.name);
      return;
    }
    onRename(trimmed);
    setEditing(false);
  }

  function confirmDelete() {
    if (window.confirm(t("profile:passkeys.confirmDelete", { name: passkey.name }))) {
      onDelete();
    }
  }

  const lastUsed = passkey.last_used_at
    ? new Date(passkey.last_used_at).toLocaleString()
    : t("profile:passkeys.lastUsedNever");

  return (
    <li className="flex flex-wrap items-center gap-3 px-3 py-2 text-sm">
      <div className="flex flex-1 min-w-[12rem] items-center gap-2">
        <KeyRound className="h-4 w-4 text-fg-subtle" aria-hidden />
        {editing ? (
          <form onSubmit={saveName} className="flex flex-1 items-center gap-2">
            <TextInput
              type="text"
              autoFocus
              value={draft}
              onChange={(e: ChangeEvent<HTMLInputElement>) => setDraft(e.target.value)}
              disabled={renaming}
              className="flex-1"
            />
            <Button type="submit" variant="primary" disabled={renaming}>
              {renaming ? t("profile:passkeys.saving") : t("common:actions.save")}
            </Button>
            <Button
              type="button"
              variant="ghost"
              onClick={() => {
                setEditing(false);
                setDraft(passkey.name);
              }}
              disabled={renaming}
            >
              {t("common:actions.cancel")}
            </Button>
          </form>
        ) : (
          <>
            <span className="font-medium text-fg">{passkey.name}</span>
            {passkey.aaguid && (
              <span className="font-mono text-xs text-fg-subtle">{passkey.aaguid.slice(0, 8)}</span>
            )}
          </>
        )}
      </div>
      {!editing && (
        <>
          <span className="text-xs text-fg-muted">
            {t("profile:passkeys.lastUsed", { value: lastUsed })}
          </span>
          <div className="flex items-center gap-1">
            <Button
              type="button"
              variant="ghost"
              onClick={() => setEditing(true)}
              disabled={renaming || deleting}
              aria-label={t("profile:passkeys.renameAria")}
            >
              <Pencil className="h-3.5 w-3.5" />
            </Button>
            <Button
              type="button"
              variant="danger"
              onClick={confirmDelete}
              disabled={renaming || deleting}
              aria-label={t("profile:passkeys.deleteAria")}
            >
              <Trash2 className="h-3.5 w-3.5" />
            </Button>
          </div>
        </>
      )}
    </li>
  );
}

function Message({ msg }: { msg: { kind: "ok" | "err"; text: string } }) {
  return msg.kind === "ok" ? <SuccessBox>{msg.text}</SuccessBox> : <ErrorBox>{msg.text}</ErrorBox>;
}

function FormFooter({
  busy,
  idle,
  busyLabel,
  msg,
  variant,
}: {
  busy: boolean;
  idle: string;
  busyLabel: string;
  msg: Msg;
  variant?: "danger";
}) {
  return (
    <div className="space-y-2">
      <Button type="submit" variant={variant === "danger" ? "danger" : "primary"} disabled={busy}>
        {busy ? busyLabel : idle}
      </Button>
      {msg && <Message msg={msg} />}
    </div>
  );
}
