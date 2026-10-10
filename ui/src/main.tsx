import { StrictMode } from "react";
import { createRoot } from "react-dom/client";
import App from "./App";
import { OwnerSessionGate } from "./OwnerSession";
import { i18nReady, useLanguage } from "./i18n";
import "./fonts.css";
import "./styles.css";

/** Re-renders the tree on a language switch so helpers that read the active language refresh too. */
function Root() {
  useLanguage();
  return <OwnerSessionGate><App /></OwnerSessionGate>;
}

// Render once the active language's catalogs are in, so the first paint is not English.
void i18nReady.finally(() => {
  createRoot(document.getElementById("root")!).render(
    <StrictMode>
      <Root />
    </StrictMode>,
  );
});

import "./interaction-system.css";
import "./settings-system.css";
import "./desktop-system.css";

import "./conversation-workspace.css";
import "./v2-foundations.css";
import "./v2-app.css";
import "./settings/settings-components.css";
import "./web-tool-steps.css";
import "./web-mention.css";
import "./web-file-drop.css";
import "./web-sidebar-list.css";
import "./chat-header.css";
import "./context-card.css";
