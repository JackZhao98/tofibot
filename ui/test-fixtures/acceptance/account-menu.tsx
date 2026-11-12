import { useState } from "react";
import { createRoot } from "react-dom/client";
import { OwnerSessionGate, SidebarAccount } from "../../src/OwnerSession";
import { HelpAndFeedback } from "../../src/settings/HelpAndFeedback";
import { i18nReady, setLanguage } from "../../src/i18n";
import "../../src/interaction-system.css";
import "../../src/v2-app.css";
import "../../src/desktop-system.css";

function Fixture() {
  const [settings, showSettings] = useState(false);
  const [mounted, setMounted] = useState(true);
  Object.assign(window, { unmountAccount: () => setMounted(false) });
  return <OwnerSessionGate>
    <div className="workspace" style={{ height: 720 }}>
      <div style={{ position: "absolute", bottom: 20, left: 20, width: 260 }}>{mounted && <SidebarAccount onSettings={() => showSettings(true)} onUsage={() => {}} onConnection={() => {}} onArtifacts={() => {}}/>}</div>
      {settings && <HelpAndFeedback/>}
    </div>
  </OwnerSessionGate>;
}
void i18nReady.then(() => setLanguage("en")).then(() => createRoot(document.getElementById("root")!).render(<Fixture/>));
