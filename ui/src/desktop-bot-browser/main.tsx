import { StrictMode } from "react";
import { createRoot } from "react-dom/client";
import { i18nReady } from "../i18n";
import { BotBrowserChrome } from "./BotBrowserChrome";
import "../cascade.css";
import "../skin/structure.css";
import "../skin/legacy.css";
import "../fonts.css";
import "../focus-ring.css";
import "./bot-browser-chrome.css";
import { applySkin } from "../skin/skin";

const media = matchMedia("(prefers-color-scheme: dark)");
// The system theme until the Mac app sends the chat page's theme with renderHost (hostBridge.ts).
const applyTheme = () => {
  if (!document.documentElement.dataset.themeFixed) applySkin(media.matches ? "dark" : "light");
};
applyTheme();
media.addEventListener("change", applyTheme);

void i18nReady.finally(() => {
  createRoot(document.getElementById("root")!).render(<StrictMode><BotBrowserChrome /></StrictMode>);
});
