import { useTranslation } from "react-i18next";
import { useEffect, useState } from "react";

import { isNamespaceLoaded, loadNamespace, type Namespace } from "./index";

// Critical namespaces (`common`, `nav`, `auth`, `ui`) are bundled with the
// entry chunk so they're always available synchronously. Lazy namespaces
// are imported on first use and registered with i18next via
// `loadNamespace`. The hook below kicks off the dynamic import the moment
// a component subscribes to a non-critical namespace, then re-renders once
// the bundle resolves so missing-key fallbacks aren't surfaced to the UI.

function isNamespace(value: string): value is Namespace {
  // Allow any string at the type level — at runtime the switch in
  // loadNamespace decides whether work is required.
  return value.length > 0;
}

// Tiny typed wrapper. Page code just calls `const { t } = useT("nav");`.
// Multiple namespaces: `useT(["nav", "common"])`.
export function useT(ns: string | string[] = "common") {
  const namespaces = Array.isArray(ns) ? ns : [ns];

  // Track which namespaces are loaded so the component re-renders once a
  // lazy bundle finishes resolving. Initial state captures the
  // synchronous answer; the effect below populates the rest.
  const [, setLoadedTick] = useState(0);

  // We re-run whenever the namespace list changes. `namespaces` is
  // re-built each render, so we depend on the canonical join — extracted
  // into a const so the hook's deps array stays statically analysable.
  const nsKey = namespaces.join(",");
  useEffect(() => {
    let cancelled = false;
    const pending: Promise<void>[] = [];
    for (const name of namespaces) {
      if (!isNamespace(name)) continue;
      if (isNamespaceLoaded(name)) continue;
      pending.push(loadNamespace(name));
    }
    if (pending.length > 0) {
      void Promise.all(pending).then(() => {
        if (!cancelled) setLoadedTick((n) => n + 1);
      });
    }
    return () => {
      cancelled = true;
    };
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [nsKey]);

  return useTranslation(ns);
}
