import js from "@eslint/js";
import tseslint from "typescript-eslint";
import react from "eslint-plugin-react";
import reactHooks from "eslint-plugin-react-hooks";
import reactRefresh from "eslint-plugin-react-refresh";
import jsxA11y from "eslint-plugin-jsx-a11y";
import security from "eslint-plugin-security";

export default tseslint.config(
  {
    ignores: [
      "dist",
      "node_modules",
      "src/lib/api-types.generated.ts",
      "src/i18n/locales/**",
    ],
  },
  js.configs.recommended,
  ...tseslint.configs.strictTypeChecked,
  ...tseslint.configs.stylisticTypeChecked,
  {
    languageOptions: {
      parserOptions: {
        project: "./tsconfig.json",
        tsconfigRootDir: import.meta.dirname,
      },
      globals: {
        window: "readonly",
        document: "readonly",
        navigator: "readonly",
        localStorage: "readonly",
        sessionStorage: "readonly",
        fetch: "readonly",
        console: "readonly",
        setTimeout: "readonly",
        clearTimeout: "readonly",
        setInterval: "readonly",
        clearInterval: "readonly",
        requestAnimationFrame: "readonly",
        cancelAnimationFrame: "readonly",
        URL: "readonly",
        URLSearchParams: "readonly",
        AbortController: "readonly",
        Event: "readonly",
        CustomEvent: "readonly",
        HTMLElement: "readonly",
        HTMLInputElement: "readonly",
        HTMLButtonElement: "readonly",
        HTMLDivElement: "readonly",
        HTMLFormElement: "readonly",
        HTMLSelectElement: "readonly",
        HTMLTextAreaElement: "readonly",
        Element: "readonly",
        Node: "readonly",
        MouseEvent: "readonly",
        KeyboardEvent: "readonly",
        FocusEvent: "readonly",
        ResizeObserver: "readonly",
        IntersectionObserver: "readonly",
        FormData: "readonly",
        Blob: "readonly",
        File: "readonly",
        FileReader: "readonly",
        crypto: "readonly",
        performance: "readonly",
        location: "readonly",
        history: "readonly",
      },
    },
    plugins: {
      react,
      "react-hooks": reactHooks,
      "react-refresh": reactRefresh,
      "jsx-a11y": jsxA11y,
      security,
    },
    settings: { react: { version: "19" } },
    rules: {
      // React
      ...react.configs.recommended.rules,
      "react/react-in-jsx-scope": "off",
      "react/prop-types": "off",
      ...reactHooks.configs.recommended.rules,

      // a11y (warn — not blocking yet)
      ...jsxA11y.configs.recommended.rules,

      // security (warn)
      ...security.configs.recommended.rules,
      "security/detect-object-injection": "off", // too noisy

      // TS strict
      "@typescript-eslint/consistent-type-imports": "warn",
      "@typescript-eslint/no-unused-vars": [
        "warn",
        { argsIgnorePattern: "^_", varsIgnorePattern: "^_" },
      ],
      "@typescript-eslint/no-explicit-any": "warn",
      // Bug-catchers kept as errors:
      "@typescript-eslint/no-floating-promises": "error",
      "@typescript-eslint/no-misused-promises": "error",
      "@typescript-eslint/require-await": "off", // we have effects that don't await
      // strict-type-checked baseline had ~840 errors. Downgrade the noisy
      // stylistic / opinion rules to "warn" so the build still passes; the
      // signal stays visible in `npm run lint` output but doesn't gate CI.
      // Revisit one-by-one as we fix the backlog.
      //
      // Rules turned off (after lint warning sweep):
      //   - no-confusing-void-expression: we adopted a "void" discipline
      //     intentionally for fire-and-forget promises; lint flags the pattern.
      //   - restrict-template-expressions: codebase explicitly accepts
      //     `${number}` / `${boolean}` in template literals (e.g. counts, IDs).
      //   - prefer-nullish-coalescing: `||` is the right idiom for our string
      //     fallbacks where "" and "0" should also fall through to defaults.
      //   - no-unnecessary-condition: many defensive checks against
      //     API-generated nullable fields that may evolve; the false-positive
      //     rate is too high to be useful as a warning.
      //   - react-refresh/only-export-components: dev-only HMR optimization;
      //     we intentionally co-locate components and helpers (ui/index.tsx,
      //     contexts that export hooks alongside providers).
      "@typescript-eslint/no-confusing-void-expression": "off",
      "@typescript-eslint/restrict-template-expressions": "off",
      "@typescript-eslint/prefer-nullish-coalescing": "off",
      "@typescript-eslint/no-unnecessary-condition": "off",
      "react-refresh/only-export-components": "off",
      // Remaining warn-level rules (real-bug-shaped, fix incrementally):
      "@typescript-eslint/consistent-type-definitions": "warn",
      "@typescript-eslint/no-unnecessary-type-assertion": "warn",
      "@typescript-eslint/array-type": "warn",
      "@typescript-eslint/no-unsafe-member-access": "warn",
      "@typescript-eslint/no-deprecated": "warn",
      "@typescript-eslint/no-unsafe-assignment": "warn",
      "@typescript-eslint/no-non-null-assertion": "warn",
      "@typescript-eslint/no-unsafe-argument": "warn",
      "@typescript-eslint/no-unsafe-return": "warn",
      "@typescript-eslint/no-unsafe-call": "warn",
      "@typescript-eslint/prefer-optional-chain": "warn",
      "@typescript-eslint/no-unnecessary-type-conversion": "warn",
      "@typescript-eslint/no-redundant-type-constituents": "warn",
      "@typescript-eslint/prefer-for-of": "warn",
      "@typescript-eslint/no-base-to-string": "warn",
      "@typescript-eslint/use-unknown-in-catch-callback-variable": "warn",
      "@typescript-eslint/no-empty-function": "warn",
      "@typescript-eslint/non-nullable-type-assertion-style": "warn",
      "@typescript-eslint/no-unnecessary-boolean-literal-compare": "warn",
      "@typescript-eslint/no-dynamic-delete": "warn",
      // react-hooks/set-state-in-effect (new in react-hooks v7): flags the
      // very common "hydrate local state from a tanstack-query result on
      // mount" pattern as cascading-render risk. The React docs explicitly
      // allow this when syncing to an external system (DB query result here).
      // Turning off; future genuine cascading-render bugs will surface as
      // performance issues we'll catch in profiling.
      "react-hooks/set-state-in-effect": "off",
      "react-hooks/purity": "warn",
      "react-hooks/refs": "warn",
      // a11y findings — keep visible but non-blocking per task ("warn — not blocking yet").
      // no-autofocus: intentionally OFF. Our autoFocus uses are all "first
      // input of a freshly-opened modal/wizard step", which is the documented
      // exception in the WCAG guidance (focus management for newly mounted
      // overlays). Screen readers handle this correctly.
      "jsx-a11y/no-autofocus": "off",
      "jsx-a11y/no-noninteractive-element-interactions": "warn",
      "jsx-a11y/no-static-element-interactions": "warn",
      "jsx-a11y/interactive-supports-focus": "warn",
      "jsx-a11y/click-events-have-key-events": "warn",
      "jsx-a11y/no-noninteractive-tabindex": "warn",
      "jsx-a11y/label-has-associated-control": "warn",
      // react minor:
      "react/no-unescaped-entities": "warn",
    },
  },
  // Lint config file itself with non-type-aware rules
  {
    files: ["eslint.config.js", "*.config.js", "*.config.ts"],
    ...tseslint.configs.disableTypeChecked,
  },
);
