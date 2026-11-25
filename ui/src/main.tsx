import "./cascade.css";
import { StrictMode } from "react";
import { createRoot } from "react-dom/client";
import App from "./App";
import { OwnerSessionGate } from "./OwnerSession";
import { BootDissolve } from "./BootScreen";
import { ProxyChallengeSlot } from "./ProxyChallengeSlot";
import { i18nReady, useLanguage } from "./i18n";
import { installFocusModality } from "./focusModality";
import { watchInstallPrompt } from "./installApp";
import "./skin/fonts.css";
import "./styles.css";

installFocusModality();
// After a server update an open page still asks for the old build's lazy chunks, which are gone: reload once into the new build
// instead of leaving a button that does nothing. The guard stops a reload loop when the chunk is missing for another reason.
window.addEventListener("vite:preloadError", event => {
  try {
    const last = Number(sessionStorage.getItem("tofi:chunk-reload") || 0);
    if (Date.now() - last < 60_000) return;
    sessionStorage.setItem("tofi:chunk-reload", String(Date.now()));
  } catch { /* no storage: reload anyway, once per page */ }
  event.preventDefault();
  window.location.reload();
});
watchInstallPrompt();

/** Re-renders the tree on a language switch so helpers that read the active language refresh too. */
function Root() {
  useLanguage();
  return <><OwnerSessionGate><App /></OwnerSessionGate><BootDissolve /><ProxyChallengeSlot /></>;
}

// Render once the active language's catalogs are in, so the first paint is not English.
void i18nReady.finally(() => {
  createRoot(document.getElementById("root")!).render(
    <StrictMode>
      <Root />
    </StrictMode>,
  );
  // The Tune panel (motion sliders) exists only in a dev build; production never bundles it.
  if (import.meta.env.DEV) void import("./dev/TuningPanel").then(module => module.mountTuningPanel());
});

import "./interaction-system.css";
import "./desktop-system.css";

import "./conversation-workspace.css";
import "./v2-foundations.css";
import "./v2-app.css";
import "./sender-groups.css";
import "./layout/settings-page.css";
import "./web-tool-steps.css";
import "./web-mention.css";
import "./web-file-drop.css";
import "./workspace/sidebar.css";
import "./context-card.css";
import "./ds/legacy-aliases.css";
import "./forced.css";
