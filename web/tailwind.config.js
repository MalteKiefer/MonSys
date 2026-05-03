/** @type {import('tailwindcss').Config} */
export default {
  content: ["./index.html", "./src/**/*.{ts,tsx}"],
  theme: {
    extend: {
      colors: {
        bg: "#09090b",
        panel: "#18181b",
        "panel-2": "#1c1c1f",
        border: "#27272a",
        "border-strong": "#3f3f46",
        fg: "#fafafa",
        "fg-muted": "#a1a1aa",
        "fg-subtle": "#71717a",
        accent: "#10b981",
        "accent-hover": "#34d399",
        ok: "#10b981",
        warn: "#f59e0b",
        fail: "#ef4444",
        stale: "#f59e0b",
        offline: "#71717a",
        critical: "#ef4444",
        info: "#60a5fa",
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
