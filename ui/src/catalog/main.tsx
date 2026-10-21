// The component catalog (docs/design/migration.md step 6). Dev only: docs/decisions/0034-component-catalog-dev-only.md.
// The sandbox must evaluate before any product module (it replaces storage and the network): keep it the first import.
import { CATALOG_MARKER, frameParams } from "./sandbox";
import { mountFrame } from "./Frame";
// The product's global stylesheets, in the order src/main.tsx imports them (scripts/test-catalog.mjs keeps the two
// lists equal), so the cascade in a frame is the cascade in the app.
import "../fonts.css";
import "../styles.css";
import "../interaction-system.css";
import "../settings-system.css";
import "../desktop-system.css";
import "../conversation-workspace.css";
import "../v2-foundations.css";
import "../v2-app.css";
import "../sender-groups.css";
import "../settings/settings-components.css";
import "../settings/settings-polish.css";
import "../web-tool-steps.css";
import "../web-mention.css";
import "../web-file-drop.css";
import "../web-sidebar-list.css";
import "../sidebar-pins.css";
import "../chat-header.css";
import "../progressive-blur.css";
import "../context-card.css";
import "../focus-ring.css";
import "./catalog.css";
import { createRoot } from "react-dom/client";
import { i18nReady } from "../i18n";
import { SECTIONS } from "./sections/index";
import { Shell, catalogIndex } from "./Shell";

// The marker comes first so that any bundle containing this entry carries it (scripts/test-catalog.mjs looks for it).
document.documentElement.dataset.catalog = CATALOG_MARKER;
if (!import.meta.env.DEV) throw new Error("The component catalog is dev only (D34).");

void i18nReady.finally(() => {
  if (frameParams.section) {
    const section = SECTIONS.find(item => item.id === frameParams.section);
    if (!section) throw new Error(`catalog: no section ${frameParams.section}`);
    mountFrame(section);
    return;
  }
  Object.assign(window, { __catalog: catalogIndex() });
  createRoot(document.getElementById("root")!).render(<Shell />);
});
