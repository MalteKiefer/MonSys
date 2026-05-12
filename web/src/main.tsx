import { StrictMode } from "react";
import { createRoot } from "react-dom/client";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { BrowserRouter } from "react-router-dom";

import "./i18n";
import { App } from "./App";
import "./index.css";
import { applyTheme, resolveInitialTheme } from "./lib/theme";

// Apply the persisted (or system-preferred) theme before React mounts so
// the first paint already has the correct palette — no flash on load.
applyTheme(resolveInitialTheme());

const qc = new QueryClient({
  defaultOptions: {
    queries: {
      staleTime: 5_000,
      retry: 1,
    },
  },
});

createRoot(document.getElementById("root")!).render(
  <StrictMode>
    <QueryClientProvider client={qc}>
      <BrowserRouter>
        <App />
      </BrowserRouter>
    </QueryClientProvider>
  </StrictMode>,
);
