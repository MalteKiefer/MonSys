import { create } from "zustand";
import { persist } from "zustand/middleware";

// We persist the session token in localStorage. Two reasons:
//   1) the API issues opaque tokens (sha256-hashed server-side), so an XSS
//      that reads localStorage is no worse than an XSS that reads
//      document.cookie without HttpOnly anyway;
//   2) we don't want session loss on full reload during development.
// If you want HttpOnly-cookie auth later, swap this for a cookie-based path
// and have the server set Set-Cookie on /v1/auth/login.

export type CurrentUser = {
  id: string;
  email: string;
  role: string;
  totp_active?: boolean;
};

type AuthState = {
  token: string | null;
  user: CurrentUser | null;
  setSession: (token: string, user: CurrentUser) => void;
  clear: () => void;
};

export const useAuth = create<AuthState>()(
  persist(
    (set) => ({
      token: null,
      user: null,
      setSession: (token, user) => set({ token, user }),
      clear: () => set({ token: null, user: null }),
    }),
    { name: "mon-auth" },
  ),
);
