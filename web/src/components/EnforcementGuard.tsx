import type { ReactNode } from "react";
import { Navigate, useLocation } from "react-router-dom";

import { useAuth } from "../lib/auth";

// Allowlist of paths the user can always reach, even when non-compliant past
// the grace period. These are exactly the surfaces they need to BECOME
// compliant (or leave): /profile (passkey + TOTP enrollment), /login, /reset.
const ALLOWED_PATHS = ["/profile", "/login", "/reset"];

function pathAllowed(pathname: string): boolean {
  return ALLOWED_PATHS.some((p) => pathname === p || pathname.startsWith(p + "/"));
}

export function EnforcementGuard({ children }: { children: ReactNode }) {
  const user = useAuth((s) => s.user);
  const location = useLocation();

  if (!user) return <>{children}</>; // unauthenticated path, App handles

  const mustEnroll = !!user.must_enroll;
  const graceUntil = user.grace_until ? new Date(user.grace_until) : null;
  const now = new Date();
  const inGrace = graceUntil !== null && now < graceUntil;

  if (mustEnroll && !inGrace && !pathAllowed(location.pathname)) {
    return <Navigate to="/profile" replace state={{ enrollmentRequired: true }} />;
  }

  return (
    <>
      {mustEnroll && inGrace && <GraceBanner graceUntil={graceUntil} />}
      {mustEnroll && !inGrace && location.pathname.startsWith("/profile") && (
        <EnrollNowBanner />
      )}
      {children}
    </>
  );
}

function GraceBanner({ graceUntil }: { graceUntil: Date }) {
  // Display-only countdown — Date.now() during render is fine, the banner
  // is re-rendered as part of normal navigation. No clock hook needed.
  // eslint-disable-next-line react-hooks/purity
  const days = Math.max(0, Math.ceil((graceUntil.getTime() - Date.now()) / 86400000));
  return (
    <div className="mx-4 mt-3 rounded-md border border-amber-400/40 bg-amber-50 px-3 py-2 text-sm text-amber-900 dark:border-amber-500/40 dark:bg-amber-950/40 dark:text-amber-100">
      <strong>Sicherheitsrichtlinie:</strong> Bitte richte innerhalb von{" "}
      <strong>{days} Tag{days === 1 ? "" : "en"}</strong> einen Passkey oder TOTP ein. Danach
      wird der Zugriff eingeschränkt, bis du eine zweite Authentifizierungsmethode
      registriert hast.
    </div>
  );
}

function EnrollNowBanner() {
  return (
    <div className="mx-4 mt-3 rounded-md border border-red-400/40 bg-red-50 px-3 py-2 text-sm text-red-900 dark:border-red-500/40 dark:bg-red-950/40 dark:text-red-100">
      <strong>Zugriff eingeschränkt:</strong> Dein Administrator verlangt eine zweite
      Authentifizierungsmethode. Registriere unten einen Passkey oder aktiviere TOTP, um
      den Zugriff wiederherzustellen.
    </div>
  );
}
