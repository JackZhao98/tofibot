import { StrictMode } from "react";
import { createRoot } from "react-dom/client";
import { i18nReady } from "../i18n";
import { BotBrowserChrome } from "./BotBrowserChrome";
import "../cascade.css";
import "../design-tokens.css";
import "../fonts.css";
import "../focus-ring.css";
import "./bot-browser-chrome.css";

const media = matchMedia("(prefers-color-scheme: dark)");
const applyTheme = () => {
  document.documentElement.dataset.theme = media.matches ? "dark" : "light";
};
applyTheme();
media.addEventListener("change", applyTheme);

void i18nReady.finally(() => {
  createRoot(document.getElementById("root")!).render(<StrictMode><BotBrowserChrome /></StrictMode>);
});
