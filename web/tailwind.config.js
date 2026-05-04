/** @type {import('tailwindcss').Config} */

// Color tokens are defined as CSS custom properties in src/index.css under
// :root (light) and .dark (dark). Each variable holds an RGB triplet (e.g.
// "9 9 11") so Tailwind's `<alpha-value>` placeholder still works for
// modifiers like `bg-ok/10`. Toggling the `dark` class on <html> swaps every
// token in one paint.
const withAlpha = (cssVar) => `rgb(var(${cssVar}) / <alpha-value>)`;

export default {
  content: ["./index.html", "./src/**/*.{ts,tsx}"],
  darkMode: "class",
  theme: {
    extend: {
      colors: {
        bg: withAlpha("--color-bg"),
        panel: withAlpha("--color-panel"),
        "panel-2": withAlpha("--color-panel-2"),
        border: withAlpha("--color-border"),
        "border-strong": withAlpha("--color-border-strong"),
        fg: withAlpha("--color-fg"),
        "fg-muted": withAlpha("--color-fg-muted"),
        "fg-subtle": withAlpha("--color-fg-subtle"),
        accent: withAlpha("--color-accent"),
        "accent-hover": withAlpha("--color-accent-hover"),
        ok: withAlpha("--color-ok"),
        warn: withAlpha("--color-warn"),
        fail: withAlpha("--color-fail"),
        stale: withAlpha("--color-stale"),
        offline: withAlpha("--color-offline"),
        critical: withAlpha("--color-critical"),
        info: withAlpha("--color-info"),
      },
      fontFamily: {
        sans: ['"Inter Variable"', "ui-sans-serif", "system-ui", "sans-serif"],
        mono: ['"JetBrains Mono Variable"', "ui-monospace", "Menlo", "Monaco", "monospace"],
      },
      fontSize: {
        // tabular numbers default for these sizes — set via class instead.
      },
      boxShadow: {
        // Subtle inset top highlight on panels — gives the "card" a tiny edge
        // without resorting to drop shadows that fight with the dark bg.
        panel: "inset 0 1px 0 0 rgba(255, 255, 255, 0.04)",
        "panel-strong": "inset 0 1px 0 0 rgba(255, 255, 255, 0.06), 0 1px 2px rgba(0, 0, 0, 0.4)",
      },
      transitionTimingFunction: {
        "ui": "cubic-bezier(0.2, 0.0, 0.0, 1.0)",
      },
      keyframes: {
        // Subtle "alive" pulse for the online status dot. Two-stage glow that
        // never fully fades so the dot stays visible even at the trough.
        pulse: {
          "0%, 100%": { boxShadow: "0 0 0 0 rgba(16, 185, 129, 0.0)" },
          "50%": { boxShadow: "0 0 0 6px rgba(16, 185, 129, 0.0)", opacity: "0.85" },
        },
        shimmer: {
          "0%": { backgroundPosition: "-400px 0" },
          "100%": { backgroundPosition: "400px 0" },
        },
      },
      animation: {
        "pulse-soft": "pulse 2.4s cubic-bezier(0.4, 0, 0.6, 1) infinite",
        "shimmer": "shimmer 1.6s linear infinite",
      },
    },
  },
  plugins: [],
};
