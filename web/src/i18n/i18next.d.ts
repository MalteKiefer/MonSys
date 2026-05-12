import "react-i18next";
import type { resources } from "./index";

declare module "react-i18next" {
  interface CustomTypeOptions {
    defaultNS: "common";
    resources: (typeof resources)["en"];
  }
}
