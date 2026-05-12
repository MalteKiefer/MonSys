import { useTranslation } from "react-i18next";

// Tiny typed wrapper. Page code just calls `const { t } = useT("nav");`.
// Multiple namespaces: `useT(["nav", "common"])`.
export function useT(ns: string | string[] = "common") {
  return useTranslation(ns);
}
