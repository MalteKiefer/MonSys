import { useAuth } from "./auth";

// Single fetch helper that:
//   - prefixes the API base ("" in prod = same origin; Vite proxies /v1 in dev)
//   - injects Authorization: Bearer <session-token> when authenticated
//   - on 401, clears the auth store so React Router can redirect to /login
//   - throws ApiError on any non-2xx so TanStack Query lands in `error` state

export class ApiError extends Error {
  constructor(public status: number, public detail: string) {
    super(`HTTP ${status}: ${detail}`);
  }
}

type Options = RequestInit & {
  // Allow callers to skip auth for the login endpoint.
  skipAuth?: boolean;
};

export async function api<T>(path: string, opts: Options = {}): Promise<T> {
  const { skipAuth, headers, ...rest } = opts;
  const token = useAuth.getState().token;

  const finalHeaders: Record<string, string> = {
    "Content-Type": "application/json",
    ...(headers as Record<string, string>),
  };
  if (!skipAuth && token) {
    finalHeaders.Authorization = `Bearer ${token}`;
  }

  const resp = await fetch(path, { ...rest, headers: finalHeaders });

  if (resp.status === 401 && !skipAuth) {
    useAuth.getState().clear();
  }

  if (!resp.ok) {
    let detail = resp.statusText;
    try {
      const body = await resp.json();
      // huma error shape: { title, status, detail, errors? }
      if (body && typeof body.detail === "string") detail = body.detail;
      else if (body && typeof body.title === "string") detail = body.title;
    } catch {
      /* fall through with statusText */
    }
    throw new ApiError(resp.status, detail);
  }

  if (resp.status === 204) return undefined as T;
  return (await resp.json()) as T;
}
