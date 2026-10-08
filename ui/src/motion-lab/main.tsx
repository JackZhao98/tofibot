// Base styles load first so each station's own CSS (imported by MotionLab)
// wins over the shared page rules at equal specificity.
import "../design-tokens.css";
import "./motion-lab.css";
import { StrictMode } from "react";
import { createRoot } from "react-dom/client";
import { MotionLab } from "./MotionLab";
import { i18nReady } from "../i18n";

// Demos reuse app cards, which read the UI catalogs; render once they load.
void i18nReady.finally(() => createRoot(document.getElementById("root")!).render(
  <StrictMode>
    <MotionLab />
  </StrictMode>,
));
