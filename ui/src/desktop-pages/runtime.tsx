import { StrictMode, type ComponentType } from "react";
import { createRoot } from "react-dom/client";
import { i18nReady, useLanguage } from "../i18n";
import "../fonts.css";
import "../styles.css";
import "../interaction-system.css";
import "../settings-system.css";
import "../desktop-system.css";
import "../v2-foundations.css";
import "../settings/settings-components.css";
import "../settings/settings-polish.css";
import "../focus-ring.css";
import "./pages.css";

export function mountPage(Page: ComponentType) {
  const preference = window.tofiDesktop?.themePreference;
  const media = matchMedia("(prefers-color-scheme: dark)");
  const apply = () => {
    if (document.documentElement.dataset.permissionGuide) return;
    const theme = preference === "light" || preference === "dark" ? preference : media.matches ? "dark" : "light";
    document.documentElement.dataset.theme = theme;
    document.documentElement.style.colorScheme = theme;
  };
  apply();
  if (preference !== "light" && preference !== "dark") media.addEventListener("change", apply);
  if (window.tofiDesktop) document.documentElement.dataset.desktop = window.tofiDesktop.platform;
  function Root() { useLanguage(); return <Page />; }
  void i18nReady.finally(() => createRoot(document.getElementById("root")!).render(<StrictMode><Root /></StrictMode>));
}
