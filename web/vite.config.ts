import { defineConfig } from "vite";
import react from "@vitejs/plugin-react";

// In dev we proxy /v1, /healthz, /readyz to the local mon-server so the SPA
// can run on Vite's default port without CORS hacks. In prod the SPA is
// served by mon-server itself, so paths are same-origin and the proxy is
// inert.
export default defineConfig({
  plugins: [react()],
  server: {
    port: 5173,
    proxy: {
      "/v1": "http://localhost:8080",
      "/healthz": "http://localhost:8080",
      "/readyz": "http://localhost:8080",
      "/docs": "http://localhost:8080",
    },
  },
  build: {
    outDir: "dist",
    sourcemap: false,
    rollupOptions: {
      output: {
        // Split heavy third-party deps into their own vendor chunks so the
        // main app bundle stays under Vite's 500 kB warning threshold. Each
        // chunk is cacheable independently — bumping React doesn't bust the
        // router chunk, etc. uPlot is shared across Dashboard / Overview /
        // host-detail Charts, so a dedicated vendor-charts chunk gives the
        // best parallel-load profile without forcing every consumer to
        // dynamic-import it.
        manualChunks: (id) => {
          if (
            id.includes("node_modules/react/") ||
            id.includes("node_modules/react-dom/") ||
            id.includes("node_modules/scheduler/")
          )
            return "vendor-react";
          if (id.includes("node_modules/react-router")) return "vendor-router";
          if (id.includes("@tanstack/react-query")) return "vendor-query";
          if (
            id.includes("node_modules/i18next") ||
            id.includes("node_modules/react-i18next")
          )
            return "vendor-i18n";
          if (id.includes("node_modules/uplot/")) return "vendor-charts";
        },
      },
    },
  },
});
