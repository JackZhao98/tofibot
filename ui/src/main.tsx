import { StrictMode } from "react";
import { createRoot } from "react-dom/client";
import App from "./App";
import { OwnerSessionGate } from "./OwnerSession";
import "./styles.css";

createRoot(document.getElementById("root")!).render(
  <StrictMode>
    <OwnerSessionGate><App /></OwnerSessionGate>
  </StrictMode>,
);

import "./interaction-system.css";
import "./settings-system.css";
import "./desktop-system.css";

import "./conversation-workspace.css";
import "./v2-foundations.css";
import "./v2-app.css";
import "./web-tool-steps.css";
import "./web-mention.css";
import "./web-file-drop.css";
import "./web-sidebar-list.css";
import "./chat-header.css";
import "./context-card.css";
