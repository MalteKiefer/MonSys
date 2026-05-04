import { useAuth } from "./auth";
import { setFailure, setSuccess } from "./connection";

// Single fetch helper that:
//   - prefixes the API base ("" in prod = same origin; Vite proxies /v1 in dev)
//   - injects Authorization: Bearer <session-token> when authenticated
//   - on 401, clears the auth store so React Router can redirect to /login
//   - throws ApiError on any non-2xx so TanStack Query lands in `error` state
//   - reports liveness to the connection store: 2xx-4xx (excl. 5xx) = success,
//     network errors and 5xx = failure (so the global "Connection lost"
//     banner can flip on after ≥ 2 failures within 10 s).

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

  let resp: Response;
  try {
    resp = await fetch(path, { ...rest, headers: finalHeaders });
  } catch (err) {
    // TypeError from fetch = network/DNS/connection-refused. The browser also
    // throws here when the request was aborted while the body was streaming.
    setFailure();
    throw err;
  }

  // 5xx is "the server is up but unhappy"; we still treat it as a connection
  // issue for the banner — a flapping backend is functionally indistinguishable
  // from a downed one for the user. 4xx is intentional and means the API is
  // alive enough to reject us, so we count those as a successful liveness ping.
  if (resp.status >= 500) {
    setFailure();
  } else {
    setSuccess();
  }

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
