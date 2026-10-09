import { createRoot } from "react-dom/client";
import { OwnerSessionGate } from "../../src/OwnerSession";
import { i18nReady } from "../../src/i18n";
import "../../src/styles.css";
import "../../src/interaction-system.css";
import "../../src/settings-system.css";
import "../../src/desktop-system.css";
import "../../src/v2-foundations.css";
import "../../src/v2-app.css";

// The browser runner intercepts every request with synthetic responses.
// Render after the browser-matched catalog loads, as main.tsx does.
void i18nReady.finally(() => createRoot(document.getElementById("root")!).render(
  <OwnerSessionGate><p>Synthetic authenticated workspace</p></OwnerSessionGate>,
));
