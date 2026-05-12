import { defineConfig } from "vite";
import react from "@vitejs/plugin-react";
import { VitePWA } from "vite-plugin-pwa";

// In dev we proxy /v1, /healthz, /readyz to the local mon-server so the SPA
// can run on Vite's default port without CORS hacks. In prod the SPA is
// served by mon-server itself, so paths are same-origin and the proxy is
// inert.
export default defineConfig({
  plugins: [
    react(),
    // PWA: emits dist/sw.js + dist/manifest.webmanifest so the SPA is
    // installable on mobile and Chrome desktop, and the shell loads
    // instantly from cache on repeat visits. API surfaces are
    // explicitly excluded — navigateFallbackDenylist keeps SPA
    // fallback from intercepting server endpoints, and a NetworkOnly
    // runtime rule for /v1/*, /healthz, /readyz, /openapi.*, /docs/*,
    // /metrics, /.well-known/* makes sure no API response ever lands
    // in the cache (auth-sensitive, freshness-sensitive). Bearer
    // tokens live in localStorage so they survive offline; the SW
    // only shells the UI.
    VitePWA({
      registerType: "autoUpdate",
      manifest: {
        name: "MonSys",
        short_name: "MonSys",
        description: "Self-hosted server monitoring control plane",
        start_url: "/",
        scope: "/",
        display: "standalone",
        background_color: "#0a0e1a",
        theme_color: "#047857",
        orientation: "any",
        lang: "en",
        icons: [
          { src: "/icons/icon-192.png", sizes: "192x192", type: "image/png", purpose: "any" },
          { src: "/icons/icon-512.png", sizes: "512x512", type: "image/png", purpose: "any" },
          { src: "/icons/icon-maskable-512.png", sizes: "512x512", type: "image/png", purpose: "maskable" },
        ],
        categories: ["productivity", "utilities", "developer"],
      },
      workbox: {
        navigateFallback: "/index.html",
        navigateFallbackDenylist: [
          /^\/v1\//,
          /^\/healthz/,
          /^\/readyz/,
          /^\/openapi\./,
          /^\/docs\//,
          /^\/metrics/,
          /^\/\.well-known\//,
        ],
        runtimeCaching: [
          // Belt-and-braces: even if a fetch from the SPA somehow
          // matches an API URL, refuse to cache. The denylist above
          // already prevents SPA-fallback interception; this catches
          // any direct fetch() inside the running app.
          {
            urlPattern: ({ url }) =>
              url.pathname.startsWith("/v1/") ||
              url.pathname === "/healthz" ||
              url.pathname === "/readyz" ||
              url.pathname.startsWith("/openapi.") ||
              url.pathname.startsWith("/docs/") ||
              url.pathname === "/metrics" ||
              url.pathname.startsWith("/.well-known/"),
            handler: "NetworkOnly",
          },
        ],
        globPatterns: ["**/*.{js,css,html,woff2,png,svg,webmanifest}"],
      },
    }),
  ],
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
