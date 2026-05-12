import { api } from "./api";
import type { LoginResponse, Passkey } from "./types";

// --- base64url helpers ------------------------------------------------------

// b64urlToBytes returns a Uint8Array backed by a fresh ArrayBuffer (not the
// default ArrayBufferLike union TS 5 widens to), so the result satisfies
// WebAuthn's BufferSource — which excludes SharedArrayBuffer.
function b64urlToBytes(s: string): Uint8Array<ArrayBuffer> {
  const pad = "=".repeat((4 - (s.length % 4)) % 4);
  const b64 = (s + pad).replace(/-/g, "+").replace(/_/g, "/");
  const raw = atob(b64);
  const buf = new ArrayBuffer(raw.length);
  const out = new Uint8Array(buf);
  for (let i = 0; i < raw.length; i++) out[i] = raw.charCodeAt(i);
  return out;
}

function bytesToB64url(buf: ArrayBuffer): string {
  const bytes = new Uint8Array(buf);
  let bin = "";
  for (const byte of bytes) bin += String.fromCharCode(byte);
  return btoa(bin).replace(/\+/g, "-").replace(/\//g, "_").replace(/=+$/, "");
}

// --- wire-format types ------------------------------------------------------
//
// The server sends WebAuthn options with all BufferSource fields as base64url
// strings. We model those as `EncodedXxx` types so the unsafe-* lint rules see
// a typed boundary instead of `any`.

interface EncodedCredentialDescriptor {
  id: string;
  type: PublicKeyCredentialType;
  transports?: AuthenticatorTransport[];
}

interface EncodedCreationOptions
  extends Omit<
    PublicKeyCredentialCreationOptions,
    "challenge" | "user" | "excludeCredentials"
  > {
  challenge: string;
  user: Omit<PublicKeyCredentialUserEntity, "id"> & { id: string };
  excludeCredentials?: EncodedCredentialDescriptor[];
}

interface EncodedRequestOptions
  extends Omit<PublicKeyCredentialRequestOptions, "challenge" | "allowCredentials"> {
  challenge: string;
  allowCredentials?: EncodedCredentialDescriptor[];
}

// Envelope shapes: huma sometimes wraps options in {publicKey: ...} per the
// WebAuthn spec, other handlers return the bare dict.
interface BeginRegisterResponse {
  challenge_token: string;
  options: EncodedCreationOptions | { publicKey: EncodedCreationOptions };
}

interface BeginLoginResponse {
  challenge_token: string;
  options: EncodedRequestOptions | { publicKey: EncodedRequestOptions };
}

function unwrapCreationOpts(
  o: EncodedCreationOptions | { publicKey: EncodedCreationOptions },
): EncodedCreationOptions {
  return "publicKey" in o ? o.publicKey : o;
}

function unwrapRequestOpts(
  o: EncodedRequestOptions | { publicKey: EncodedRequestOptions },
): EncodedRequestOptions {
  return "publicKey" in o ? o.publicKey : o;
}

// PublicKeyCredentialCreationOptions/RequestOptions arrive with `challenge`,
// `user.id`, and `excludeCredentials[*].id` / `allowCredentials[*].id` as
// base64url strings. The browser API expects BufferSources.
function decodeCreationOptions(
  opts: EncodedCreationOptions,
): PublicKeyCredentialCreationOptions {
  return {
    ...opts,
    challenge: b64urlToBytes(opts.challenge),
    user: { ...opts.user, id: b64urlToBytes(opts.user.id) },
    excludeCredentials: opts.excludeCredentials?.map((c) => ({
      ...c,
      id: b64urlToBytes(c.id),
    })),
  };
}

function decodeRequestOptions(
  opts: EncodedRequestOptions,
): PublicKeyCredentialRequestOptions {
  return {
    ...opts,
    challenge: b64urlToBytes(opts.challenge),
    allowCredentials: opts.allowCredentials?.map((c) => ({
      ...c,
      id: b64urlToBytes(c.id),
    })),
  };
}

// Encoded credential payloads sent back to the server.
interface EncodedCreateCredential {
  id: string;
  rawId: string;
  type: string;
  response: {
    attestationObject: string;
    clientDataJSON: string;
    transports: string[];
  };
  clientExtensionResults: AuthenticationExtensionsClientOutputs;
  authenticatorAttachment: string | null;
}

interface EncodedGetCredential {
  id: string;
  rawId: string;
  type: string;
  response: {
    authenticatorData: string;
    clientDataJSON: string;
    signature: string;
    userHandle: string | null;
  };
  clientExtensionResults: AuthenticationExtensionsClientOutputs;
  authenticatorAttachment: string | null;
}

// The DOM lib types getTransports on AuthenticatorAttestationResponse, but it's
// behind a feature check in some browsers. Define an optional-method shape so
// we can call it without `any`.
type AttestationWithTransports = AuthenticatorAttestationResponse & {
  getTransports?: () => string[];
};

// PublicKeyCredential.authenticatorAttachment is a newer field; same pattern.
type CredentialWithAttachment = PublicKeyCredential & {
  authenticatorAttachment?: string | null;
};

function encodeCreate(cred: PublicKeyCredential): EncodedCreateCredential {
  const resp = cred.response as AttestationWithTransports;
  const credWithAttachment = cred as CredentialWithAttachment;
  return {
    id: cred.id,
    rawId: bytesToB64url(cred.rawId),
    type: cred.type,
    response: {
      attestationObject: bytesToB64url(resp.attestationObject),
      clientDataJSON: bytesToB64url(resp.clientDataJSON),
      transports: resp.getTransports?.() ?? [],
    },
    clientExtensionResults: cred.getClientExtensionResults(),
    authenticatorAttachment: credWithAttachment.authenticatorAttachment ?? null,
  };
}

function encodeGet(cred: PublicKeyCredential): EncodedGetCredential {
  const resp = cred.response as AuthenticatorAssertionResponse;
  const credWithAttachment = cred as CredentialWithAttachment;
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
    clientExtensionResults: cred.getClientExtensionResults(),
    authenticatorAttachment: credWithAttachment.authenticatorAttachment ?? null,
  };
}

// --- public API -------------------------------------------------------------

export function supported(): boolean {
  return typeof window !== "undefined" && !!window.PublicKeyCredential;
}

export async function registerPasskey(name: string): Promise<Passkey> {
  if (!supported()) throw new Error("This browser does not support passkeys.");

  const begin = await api<BeginRegisterResponse>(
    "/v1/auth/webauthn/register/begin",
    { method: "POST", body: JSON.stringify({ name }) },
  );

  const opts = decodeCreationOptions(unwrapCreationOpts(begin.options));
  const cred = (await navigator.credentials.create({
    publicKey: opts,
  })) as PublicKeyCredential | null;
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
export async function loginWithPasskey(
  opts: { conditional?: boolean } = {},
): Promise<LoginResponse> {
  if (!supported()) throw new Error("This browser does not support passkeys.");

  const begin = await api<BeginLoginResponse>("/v1/auth/webauthn/login/begin", {
    method: "POST",
    skipAuth: true,
    body: "{}",
  });

  const reqOpts = decodeRequestOptions(unwrapRequestOpts(begin.options));
  const cred = (await navigator.credentials.get({
    publicKey: reqOpts,
    mediation: opts.conditional ? "conditional" : undefined,
  })) as PublicKeyCredential | null;
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
export async function conditionalAutofill(
  signal?: AbortSignal,
): Promise<LoginResponse | null> {
  if (!supported()) return null;
  if (!("isConditionalMediationAvailable" in PublicKeyCredential)) return null;
  // isConditionalMediationAvailable is a static method on PublicKeyCredential
  // that some browsers don't expose yet. Feature-detect, then call via the
  // typed shape rather than `any`.
  const isAvail = (
    PublicKeyCredential as unknown as {
      isConditionalMediationAvailable?: () => Promise<boolean>;
    }
  ).isConditionalMediationAvailable;
  const avail = isAvail ? await isAvail.call(PublicKeyCredential) : false;
  if (!avail) return null;
  try {
    const begin = await api<BeginLoginResponse>(
      "/v1/auth/webauthn/login/begin",
      { method: "POST", skipAuth: true, body: "{}" },
    );
    const reqOpts = decodeRequestOptions(unwrapRequestOpts(begin.options));
    const cred = (await navigator.credentials.get({
      publicKey: reqOpts,
      mediation: "conditional",
      signal,
    })) as PublicKeyCredential | null;
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
    if (err instanceof Error && err.name === "AbortError") return null;
    return null;
  }
}
