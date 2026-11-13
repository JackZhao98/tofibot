import { StrictMode } from "react";
import { createRoot } from "react-dom/client";
import { i18nReady, useLanguage } from "../i18n";
import { MacSettingsPage } from "./MacSettingsPage";
import "../cascade.css";
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
import "../sidebar-rail.css";
import "../chat-header.css";
import "../progressive-blur.css";
import "../context-card.css";
import "../focus-ring.css";
import "./mac-settings.css";

// The desktop app picks the colour scheme before the page loads (--tofi-appearance); "system" follows the OS.
// The workspace's own appearance hook is not used here: it reads page storage and calls workspace-only IPC.
const preference = window.tofiDesktop?.themePreference;
const media = matchMedia("(prefers-color-scheme: dark)");
const applyTheme = () => {
  const theme = preference === "light" || preference === "dark" ? preference : media.matches ? "dark" : "light";
  document.documentElement.dataset.theme = theme;
  document.documentElement.style.colorScheme = theme;
};
applyTheme();
if (preference !== "light" && preference !== "dark") media.addEventListener("change", applyTheme);
if (window.tofiDesktop) document.documentElement.dataset.desktop = window.tofiDesktop.platform;

function Root() {
  useLanguage();
  return <MacSettingsPage />;
}

// Render once the active language's catalogs are in, so the first paint is not English.
void i18nReady.finally(() => {
  createRoot(document.getElementById("root")!).render(<StrictMode><Root /></StrictMode>);
});
