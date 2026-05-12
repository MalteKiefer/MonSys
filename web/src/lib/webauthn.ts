import { api } from "./api";
import { LoginResponse, Passkey } from "./types";

// --- base64url helpers ------------------------------------------------------

function b64urlToBytes(s: string): Uint8Array {
  const pad = "=".repeat((4 - (s.length % 4)) % 4);
  const b64 = (s + pad).replace(/-/g, "+").replace(/_/g, "/");
  const raw = atob(b64);
  const out = new Uint8Array(raw.length);
  for (let i = 0; i < raw.length; i++) out[i] = raw.charCodeAt(i);
  return out;
}

function bytesToB64url(buf: ArrayBuffer): string {
  const bytes = new Uint8Array(buf);
  let bin = "";
  for (let i = 0; i < bytes.length; i++) bin += String.fromCharCode(bytes[i]);
  return btoa(bin).replace(/\+/g, "-").replace(/\//g, "_").replace(/=+$/, "");
}

// PublicKeyCredentialCreationOptions/RequestOptions arrive with `challenge`,
// `user.id`, and `excludeCredentials[*].id` / `allowCredentials[*].id` as
// base64url strings. The browser API expects BufferSources. Walk the dict and
// convert in-place.
function decodeCreationOptions(opts: any): PublicKeyCredentialCreationOptions {
  const o = { ...opts };
  o.challenge = b64urlToBytes(o.challenge);
  if (o.user?.id) o.user = { ...o.user, id: b64urlToBytes(o.user.id) };
  if (Array.isArray(o.excludeCredentials)) {
    o.excludeCredentials = o.excludeCredentials.map((c: any) => ({
      ...c,
      id: b64urlToBytes(c.id),
    }));
  }
  return o as PublicKeyCredentialCreationOptions;
}

function decodeRequestOptions(opts: any): PublicKeyCredentialRequestOptions {
  const o = { ...opts };
  o.challenge = b64urlToBytes(o.challenge);
  if (Array.isArray(o.allowCredentials)) {
    o.allowCredentials = o.allowCredentials.map((c: any) => ({
      ...c,
      id: b64urlToBytes(c.id),
    }));
  }
  return o as PublicKeyCredentialRequestOptions;
}

// Encode the AuthenticatorResponse fields the server expects (base64url).
function encodeCreate(cred: PublicKeyCredential): any {
  const resp = cred.response as AuthenticatorAttestationResponse;
  return {
    id: cred.id,
    rawId: bytesToB64url(cred.rawId),
    type: cred.type,
    response: {
      attestationObject: bytesToB64url(resp.attestationObject),
      clientDataJSON: bytesToB64url(resp.clientDataJSON),
      transports: (resp as any).getTransports?.() ?? [],
    },
    clientExtensionResults: cred.getClientExtensionResults?.() ?? {},
    authenticatorAttachment: (cred as any).authenticatorAttachment ?? null,
  };
}

function encodeGet(cred: PublicKeyCredential): any {
  const resp = cred.response as AuthenticatorAssertionResponse;
  return {
    id: cred.id,
    rawId: bytesToB64url(cred.rawId),
    type: cred.type,
    response: {
      authenticatorData: bytesToB64url(resp.authenticatorData),
      clientDataJSON: bytesToB64url(resp.clientDataJSON),
      signature: bytesToB64url(resp.signature),
      userHandle: resp.userHandle ? bytesToB64url(resp.userHandle) : null,
    },
    clientExtensionResults: cred.getClientExtensionResults?.() ?? {},
    authenticatorAttachment: (cred as any).authenticatorAttachment ?? null,
  };
}

// --- public API -------------------------------------------------------------

export function supported(): boolean {
  return typeof window !== "undefined" && !!window.PublicKeyCredential;
}

export async function registerPasskey(name: string): Promise<Passkey> {
  if (!supported()) throw new Error("This browser does not support passkeys.");

  const begin = await api<{ challenge_token: string; options: any }>(
    "/v1/auth/webauthn/register/begin",
    { method: "POST", body: JSON.stringify({ name }) },
  );

  // Some huma payloads wrap PublicKeyCredentialCreationOptions in a
  // {"publicKey": {...}} envelope per the WebAuthn spec, others return the
  // options dict directly. Unwrap either shape.
  const optsRaw = begin.options?.publicKey ?? begin.options;
  const opts = decodeCreationOptions(optsRaw);
  const cred = (await navigator.credentials.create({ publicKey: opts })) as PublicKeyCredential | null;
  if (!cred) throw new Error("Authenticator returned no credential.");

  return api<Passkey>("/v1/auth/webauthn/register/finish", {
    method: "POST",
    body: JSON.stringify({
      challenge_token: begin.challenge_token,
      name,
      credential: encodeCreate(cred),
    }),
  });
}

// loginWithPasskey runs the discoverable-credential ("usernameless") flow.
// Pass mediation: "conditional" to enable autofill on the email input — call
// the helper at page mount with conditional=true; the browser only completes
// if the user explicitly picks a passkey from the autofill UI.
export async function loginWithPasskey(opts: { conditional?: boolean } = {}): Promise<LoginResponse> {
  if (!supported()) throw new Error("This browser does not support passkeys.");

  const begin = await api<{ challenge_token: string; options: any }>(
    "/v1/auth/webauthn/login/begin",
    { method: "POST", skipAuth: true, body: "{}" },
  );

  const optsRaw = begin.options?.publicKey ?? begin.options;
  const reqOpts = decodeRequestOptions(optsRaw);
  const cred = (await navigator.credentials.get({
    publicKey: reqOpts,
    mediation: opts.conditional ? "conditional" : undefined,
  } as any)) as PublicKeyCredential | null;
  if (!cred) throw new Error("Authenticator returned no credential.");

  return api<LoginResponse>("/v1/auth/webauthn/login/finish", {
    method: "POST",
    skipAuth: true,
    body: JSON.stringify({
      challenge_token: begin.challenge_token,
      credential: encodeGet(cred),
    }),
  });
}

// Conditional-UI hot path: kick off a discoverable get with mediation:conditional
// at page mount. Returns a promise that resolves only when the user picks a
// passkey from autofill. Cancels itself via AbortSignal if you pass one.
export async function conditionalAutofill(signal?: AbortSignal): Promise<LoginResponse | null> {
  if (!supported()) return null;
  if (!("isConditionalMediationAvailable" in PublicKeyCredential)) return null;
  const avail = await (PublicKeyCredential as any).isConditionalMediationAvailable?.();
  if (!avail) return null;
  try {
    const begin = await api<{ challenge_token: string; options: any }>(
      "/v1/auth/webauthn/login/begin",
      { method: "POST", skipAuth: true, body: "{}" },
    );
    const optsRaw = begin.options?.publicKey ?? begin.options;
    const reqOpts = decodeRequestOptions(optsRaw);
    const cred = (await navigator.credentials.get({
      publicKey: reqOpts,
      mediation: "conditional",
      signal,
    } as any)) as PublicKeyCredential | null;
    if (!cred) return null;
    return await api<LoginResponse>("/v1/auth/webauthn/login/finish", {
      method: "POST",
      skipAuth: true,
      body: JSON.stringify({
        challenge_token: begin.challenge_token,
        credential: encodeGet(cred),
      }),
    });
  } catch (err) {
    if ((err as any)?.name === "AbortError") return null;
    return null;
  }
}
