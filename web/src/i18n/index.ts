import i18n from "i18next";
import { initReactI18next } from "react-i18next";
import LanguageDetector from "i18next-browser-languagedetector";

// Critical-path namespaces — these are loaded eagerly because they're
// referenced from the auth-gate routes (Login, Reset, ConfirmEmail) and
// from the top-level App shell. Keeping them in the entry chunk avoids a
// flash of missing translations on first paint.
import enCommon from "./locales/en/common.json";
import enNav from "./locales/en/nav.json";
import enAuth from "./locales/en/auth.json";
import enUi from "./locales/en/ui.json";
import deCommon from "./locales/de/common.json";
import deNav from "./locales/de/nav.json";
import deAuth from "./locales/de/auth.json";
import deUi from "./locales/de/ui.json";

export const SUPPORTED_LOCALES = ["en", "de"] as const;
export type SupportedLocale = (typeof SUPPORTED_LOCALES)[number];

// The full namespace list is kept as a const so the lazy loader and the
// i18next config below stay in sync. New namespaces must be added here AND
// to the dynamic `loadNamespace` switch below.
export const ALL_NAMESPACES = [
  "common",
  "nav",
  "auth",
  "ui",
  "dashboard",
  "hosts",
  "packages",
  "profile",
  "admin",
  "notifications",
  "hostDetail",
] as const;
export type Namespace = (typeof ALL_NAMESPACES)[number];

const CRITICAL_NAMESPACES = ["common", "nav", "auth", "ui"] as const satisfies readonly Namespace[];

// Eagerly-bundled resource bag. Heavy namespaces (dashboard, hosts, admin,
// notifications, etc.) are loaded on demand by `useT()` via
// `loadNamespace` — see useT.ts.
export const resources = {
  en: {
    common: enCommon,
    nav: enNav,
    auth: enAuth,
    ui: enUi,
  },
  de: {
    common: deCommon,
    nav: deNav,
    auth: deAuth,
    ui: deUi,
  },
} as const;

void i18n
  .use(LanguageDetector)
  .use(initReactI18next)
  .init({
    resources,
    fallbackLng: "en",
    supportedLngs: SUPPORTED_LOCALES,
    defaultNS: "common",
    // Only the critical namespaces are pre-registered so i18next doesn't
    // try to resolve from missing bags before they're lazy-loaded. The
    // rest are added via `i18n.addResourceBundle` from `loadNamespace`.
    ns: [...CRITICAL_NAMESPACES],
    interpolation: { escapeValue: false },
    detection: {
      // The preference cascade. localStorage wins (lets the Profile setting
      // stick) → navigator (browser language). Persist what the detector
      // settled on so the next paint is correct.
      order: ["localStorage", "navigator"],
      lookupLocalStorage: "mon-lang",
      caches: ["localStorage"],
    },
  });

export default i18n;

// loadNamespace dynamically imports the JSON resources for a non-critical
// namespace on first use and registers them with i18next under all
// supported locales. The bundler turns each `import()` into its own chunk
// (one per namespace × locale), so the login bundle no longer drags the
// admin or notifications translation tables along for the ride. Calls are
// cached so a second `useT("admin")` is a no-op.
const loadedNamespaces = new Set<Namespace>(CRITICAL_NAMESPACES);
const inflight = new Map<Namespace, Promise<void>>();

export function isNamespaceLoaded(ns: Namespace): boolean {
  return loadedNamespaces.has(ns);
}

export function loadNamespace(ns: Namespace): Promise<void> {
  if (loadedNamespaces.has(ns)) return Promise.resolve();
  const existing = inflight.get(ns);
  if (existing) return existing;

  const promise = (async () => {
    let en: Record<string, unknown> = {};
    let de: Record<string, unknown> = {};
    // Vite needs static-shaped dynamic imports to emit separate chunks per
    // namespace; a switch on a literal namespace name does the job
    // without forcing every JSON into the manifest.
    switch (ns) {
      case "dashboard":
        en = (await import("./locales/en/dashboard.json")).default;
        de = (await import("./locales/de/dashboard.json")).default;
        break;
      case "hosts":
        en = (await import("./locales/en/hosts.json")).default;
        de = (await import("./locales/de/hosts.json")).default;
        break;
      case "packages":
        en = (await import("./locales/en/packages.json")).default;
        de = (await import("./locales/de/packages.json")).default;
        break;
      case "profile":
        en = (await import("./locales/en/profile.json")).default;
        de = (await import("./locales/de/profile.json")).default;
        break;
      case "admin":
        en = (await import("./locales/en/admin.json")).default;
        de = (await import("./locales/de/admin.json")).default;
        break;
      case "notifications":
        en = (await import("./locales/en/notifications.json")).default;
        de = (await import("./locales/de/notifications.json")).default;
        break;
      case "hostDetail":
        en = (await import("./locales/en/hostDetail.json")).default;
        de = (await import("./locales/de/hostDetail.json")).default;
        break;
      default:
        // Critical namespaces or unknown — nothing to do.
        return;
    }
    i18n.addResourceBundle("en", ns, en, true, true);
    i18n.addResourceBundle("de", ns, de, true, true);
    loadedNamespaces.add(ns);
  })();
  inflight.set(ns, promise);
  return promise;
}

// setLocale is called from the Profile screen + the TopBar selector. Pushes
// through i18next AND updates localStorage so first paint of the next route
// change uses the new locale.
export function setLocale(loc: SupportedLocale) {
  void i18n.changeLanguage(loc);
  try {
    localStorage.setItem("mon-lang", loc);
  } catch {
    /* private browsing — no-op */
  }
}
