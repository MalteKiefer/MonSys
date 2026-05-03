/** @type {import('tailwindcss').Config} */
export default {
  content: ["./index.html", "./src/**/*.{ts,tsx}"],
  theme: {
    extend: {
      colors: {
        // Status palette mirrored on the server: probe.StatusOK / liveness.StatusOnline.
        ok: "#22c55e",
        warn: "#eab308",
        fail: "#ef4444",
        stale: "#f59e0b",
        offline: "#6b7280",
      },
    },
  },
  plugins: [],
};
