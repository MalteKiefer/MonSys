import i18n from "i18next";
import { initReactI18next } from "react-i18next";
import LanguageDetector from "i18next-browser-languagedetector";

// Bundle every namespace at build time (vite picks them up via the explicit
// imports). Code-splitting locales is not worth the complexity at this size.
import enCommon from "./locales/en/common.json";
import enNav from "./locales/en/nav.json";
import enAuth from "./locales/en/auth.json";
import enDashboard from "./locales/en/dashboard.json";
import enHosts from "./locales/en/hosts.json";
import enPackages from "./locales/en/packages.json";
import enProfile from "./locales/en/profile.json";
import enAdmin from "./locales/en/admin.json";
import enNotifications from "./locales/en/notifications.json";
import enHostDetail from "./locales/en/hostDetail.json";
import enUi from "./locales/en/ui.json";

import deCommon from "./locales/de/common.json";
import deNav from "./locales/de/nav.json";
import deAuth from "./locales/de/auth.json";
import deDashboard from "./locales/de/dashboard.json";
import deHosts from "./locales/de/hosts.json";
import dePackages from "./locales/de/packages.json";
import deProfile from "./locales/de/profile.json";
import deAdmin from "./locales/de/admin.json";
import deNotifications from "./locales/de/notifications.json";
import deHostDetail from "./locales/de/hostDetail.json";
import deUi from "./locales/de/ui.json";

export const SUPPORTED_LOCALES = ["en", "de"] as const;
export type SupportedLocale = (typeof SUPPORTED_LOCALES)[number];

export const resources = {
  en: {
    common: enCommon,
    nav: enNav,
    auth: enAuth,
    dashboard: enDashboard,
    hosts: enHosts,
    packages: enPackages,
    profile: enProfile,
    admin: enAdmin,
    notifications: enNotifications,
    hostDetail: enHostDetail,
    ui: enUi,
  },
  de: {
    common: deCommon,
    nav: deNav,
    auth: deAuth,
    dashboard: deDashboard,
    hosts: deHosts,
    packages: dePackages,
    profile: deProfile,
    admin: deAdmin,
    notifications: deNotifications,
    hostDetail: deHostDetail,
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
    ns: Object.keys(resources.en),
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
