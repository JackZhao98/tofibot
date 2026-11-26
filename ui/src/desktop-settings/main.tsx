import "../cascade.css";
import { StrictMode } from "react";
import { createRoot } from "react-dom/client";
import { i18nReady, useLanguage } from "../i18n";
import { MacSettingsPage } from "./MacSettingsPage";
import "../skin/fonts.css";
import "../styles.css";
import "../skin/default.css";
import "../desktop-system.css";

import "../conversation-workspace.css";
import "../sender-groups.css";
import "../layout/settings-page.css";
import "../web-tool-steps.css";
import "../web-mention.css";
import "../web-file-drop.css";
import "../workspace/sidebar.css";
import "../context-card.css";
import "../forced.css";
import "./mac-settings.css";
import { applySkin } from "../skin/skin";

// The desktop app picks the colour scheme before the page loads (--tofi-appearance); "system" follows the OS.
// The workspace's own appearance hook is not used here: it reads page storage and calls workspace-only IPC.
const preference = window.tofiDesktop?.themePreference;
const media = matchMedia("(prefers-color-scheme: dark)");
const applyTheme = () => {
  const theme = preference === "light" || preference === "dark" ? preference : media.matches ? "dark" : "light";
  applySkin(theme);
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
