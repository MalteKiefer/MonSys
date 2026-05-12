import { Moon, Sun } from "lucide-react";
import { useEffect, useState } from "react";

import type { Theme } from "../lib/theme";
import { applyTheme, persistTheme, resolveInitialTheme } from "../lib/theme";
import { useT } from "../i18n/useT";

// Small icon button that flips the app palette between dark and light.
// State lives on <html class="dark">; this component owns its mirror copy
// only so the rendered icon stays in sync. localStorage persistence is
// handled in lib/theme so the early bootstrap and the toggle agree.
export function ThemeToggle() {
  const { t } = useT("nav");
  const [theme, setTheme] = useState<Theme>(() => resolveInitialTheme());

  useEffect(() => {
    applyTheme(theme);
  }, [theme]);

  function toggle() {
    const next: Theme = theme === "dark" ? "light" : "dark";
    setTheme(next);
    persistTheme(next);
  }

  const isDark = theme === "dark";
  const label = isDark ? t("actions.switch_to_light") : t("actions.switch_to_dark");
  const Icon = isDark ? Sun : Moon;

  return (
    <button
      type="button"
      onClick={toggle}
      aria-label={label}
      title={label}
      className="inline-flex h-7 w-7 items-center justify-center rounded-md border border-border bg-panel text-fg-muted transition-colors hover:bg-panel-2 hover:text-fg focus:outline-none focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-accent/40"
    >
      <Icon className="h-3.5 w-3.5" />
    </button>
  );
}
