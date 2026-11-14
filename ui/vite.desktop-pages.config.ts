import { fileURLToPath } from "node:url";
import { defineConfig } from "vite";
import react from "@vitejs/plugin-react";

// Desktop pages build separately after the workspace, preserving its chunks and shell budget.
// The Mac app serves each entry at its canonical /__desktop/<page> route.
export default defineConfig({
  plugins: [react()],
  build: {
    outDir: "dist",
    emptyOutDir: false,
    rolldownOptions: {
      input: {
        "desktop-permission-guide": fileURLToPath(new URL("desktop-permission-guide.html", import.meta.url)),
        "desktop-local-run": fileURLToPath(new URL("desktop-local-run.html", import.meta.url)),
        "desktop-update-required": fileURLToPath(new URL("desktop-update-required.html", import.meta.url)),
        "desktop-setup": fileURLToPath(new URL("desktop-setup.html", import.meta.url)),
        "desktop-settings": fileURLToPath(new URL("desktop-settings.html", import.meta.url)),
        "desktop-bot-browser": fileURLToPath(new URL("desktop-bot-browser.html", import.meta.url)),
      },
    },
  },
});
