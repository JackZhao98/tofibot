import { fileURLToPath } from "node:url";
import { defineConfig } from "vite";
import react from "@vitejs/plugin-react";

// Desktop pages build separately after the workspace, preserving its chunks and shell budget.
// The Mac app serves these entries at /__desktop/settings and /__desktop/bot-browser.
export default defineConfig({
  plugins: [react()],
  build: {
    outDir: "dist",
    emptyOutDir: false,
    rolldownOptions: {
      input: {
        "desktop-settings": fileURLToPath(new URL("desktop-settings.html", import.meta.url)),
        "desktop-bot-browser": fileURLToPath(new URL("desktop-bot-browser.html", import.meta.url)),
      },
    },
  },
});
