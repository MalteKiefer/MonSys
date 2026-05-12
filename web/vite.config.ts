import { defineConfig, type Plugin } from "vite";
import react from "@vitejs/plugin-react";
import { VitePWA } from "vite-plugin-pwa";

// Tiny transform to make the entry stylesheet non-render-blocking.
// 1. Emits a `<link rel="preload" as="style">` so the browser fetches the
//    CSS at high priority alongside the JS entry, instead of waiting for it
//    to be discovered after the head parses.
// 2. Switches the original `<link rel="stylesheet">` to
//    `media="not all"` so it does NOT block the renderer; a small inline
//    bootstrap script promotes it to `all` on DOMContentLoaded (or
//    immediately if the document is already interactive).
// 3. A `<noscript>` fallback restores the original blocking stylesheet
//    when JS is disabled so progressive-enhancement still paints the right
//    palette.
//
// The SPA's mount point (`<div id="root">`) is empty until React mounts,
// so the typical FOUC concern with async CSS does not apply — the
// unstyled empty container is invisible regardless. By the time React's
// first paint happens, the bootstrap script has already promoted the
// stylesheet's media list, so the page paints fully styled.
//
// The bootstrap script is emitted as an external module asset
// (`/assets/css-bootstrap-*.js`) so it satisfies the production
// `script-src 'self'` Content-Security-Policy with no `'unsafe-inline'`
// concession. (The CSP also allows `style-src 'self' 'unsafe-inline'`,
// which is unrelated; we don't ship inline scripts.)
function nonBlockingStylesheet(): Plugin {
  // The bootstrap source is short enough to inline as a string here. Vite
  // emits it as a hashed asset that the index.html references via a
  // module-typed script tag. Because the tag itself loads asynchronously
  // (type=module is deferred by default), it can't itself become render-
  // blocking either.
  const BOOTSTRAP_SRC = [
    "// Promote any media='not all' stylesheets to media='all'. This is the",
    "// counterpart of the non-blocking-stylesheet vite plugin — keeping the",
    "// promotion in an external file lets us comply with a strict",
    "// script-src 'self' CSP (no inline onload handlers required).",
    "function promote(){",
    "  for (const link of document.querySelectorAll('link[rel=stylesheet][media=\"not all\"]')) {",
    "    link.media = 'all';",
    "  }",
    "}",
    "if (document.readyState === 'loading') {",
    "  document.addEventListener('DOMContentLoaded', promote, { once: true });",
    "} else {",
    "  promote();",
    "}",
  ].join("\n");

  let bootstrapHref: string | null = null;
  return {
    name: "non-blocking-stylesheet",
    // Emit the bootstrap as a hashed asset so it cache-busts independently
    // of the main entry.
    generateBundle(_options, bundle) {
      const ref = this.emitFile({
        type: "asset",
        name: "css-bootstrap.js",
        source: BOOTSTRAP_SRC,
      });
      // Locate the emitted asset's final filename so we can reference it
      // from the HTML. rollup's `getFileName` is the canonical lookup.
      bootstrapHref = "/" + this.getFileName(ref);
      // Touch `bundle` so TS doesn't complain about unused — the emit
      // already mutated it.
      void bundle;
    },
    transformIndexHtml(html) {
      return html.replace(
        /<link\s+rel="stylesheet"([^>]*?)>/g,
        (_match, attrs: string) => {
          const cleaned = attrs.trim();
          const bootstrap = bootstrapHref
            ? `<script type="module" src="${bootstrapHref}"></script>`
            : "";
          return [
            `<link rel="preload" as="style" ${cleaned}>`,
            `<link rel="stylesheet" ${cleaned} media="not all">`,
            `<noscript><link rel="stylesheet" ${cleaned}></noscript>`,
            bootstrap,
          ].join("\n    ");
        },
      );
    },
  };
}

// In dev we proxy /v1, /healthz, /readyz to the local mon-server so the SPA
// can run on Vite's default port without CORS hacks. In prod the SPA is
// served by mon-server itself, so paths are same-origin and the proxy is
// inert.
export default defineConfig({
  plugins: [
    react(),
    nonBlockingStylesheet(),
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
