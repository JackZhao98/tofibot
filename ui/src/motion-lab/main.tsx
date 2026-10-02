// Base styles load first so each station's own CSS (imported by MotionLab)
// wins over the shared page rules at equal specificity.
import "../design-tokens.css";
import "./motion-lab.css";
import { StrictMode } from "react";
import { createRoot } from "react-dom/client";
import { MotionLab } from "./MotionLab";

createRoot(document.getElementById("root")!).render(
  <StrictMode>
    <MotionLab />
  </StrictMode>,
);
