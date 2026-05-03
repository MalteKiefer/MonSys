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
  },
});
