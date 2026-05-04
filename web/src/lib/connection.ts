// A tiny event bus that tracks whether the API is reachable.
//
// We keep this deliberately framework-light (no zustand, no react-query) so
// the api() fetch helper can update it without pulling React into a module
// that is also imported from non-component code.
//
// Behaviour:
//   - setSuccess()        → flips status to "ok" and resets the failure window.
//   - setFailure()        → counts consecutive failures inside a 10s window
//                            and only flips to "lost" once we've seen ≥ 2.
//                            One-off transient failures don't flash the banner.
//   - setOffline()        → unconditional flip to "lost" (used by the
//                            window 'offline' event so the OS network drop
//                            is reflected immediately).
//   - subscribe(cb)       → React-friendly subscription used by useSyncExternalStore.

export type ConnectionStatus = "ok" | "lost";

const FAILURE_WINDOW_MS = 10_000;
const FAILURE_THRESHOLD = 2;

let status: ConnectionStatus = "ok";
let failureCount = 0;
let firstFailureAt = 0;
const listeners = new Set<() => void>();

function emit() {
  for (const cb of listeners) cb();
}

function setStatus(next: ConnectionStatus) {
  if (status === next) return;
  status = next;
  emit();
}

export function getConnectionStatus(): ConnectionStatus {
  return status;
}

export function setSuccess(): void {
  failureCount = 0;
  firstFailureAt = 0;
  setStatus("ok");
}

export function setFailure(): void {
  const now = Date.now();
  if (failureCount === 0 || now - firstFailureAt > FAILURE_WINDOW_MS) {
    // Either the first failure ever, or the previous window expired. Restart.
    failureCount = 1;
    firstFailureAt = now;
    return;
  }
  failureCount += 1;
  if (failureCount >= FAILURE_THRESHOLD) {
    setStatus("lost");
  }
}

export function setOffline(): void {
  // Skip the throttle: the OS told us we have no network at all.
  failureCount = FAILURE_THRESHOLD;
  firstFailureAt = Date.now();
  setStatus("lost");
}

export function subscribe(cb: () => void): () => void {
  listeners.add(cb);
  return () => {
    listeners.delete(cb);
  };
}

// Wire OS-level online/offline events once at module load. Guarded so SSR
// or test environments without window don't blow up.
if (typeof window !== "undefined") {
  window.addEventListener("offline", () => {
    setOffline();
  });
  window.addEventListener("online", () => {
    // We don't claim "ok" until the next real fetch succeeds — the network
    // could be up but the server still down. Just reset the failure window
    // so the banner clears on the next 200.
    failureCount = 0;
    firstFailureAt = 0;
  });
}
