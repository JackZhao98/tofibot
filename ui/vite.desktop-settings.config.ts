import { fileURLToPath } from "node:url";
import { defineConfig } from "vite";
import react from "@vitejs/plugin-react";

// The desktop app's Mac settings page builds after the app, into the same dist/, as a separate bundle so the
// workspace's own chunks and file names stay exactly as they were (test:shell-budget). The desktop app serves
// desktop-settings.html at /__desktop/settings only; in development the main dev server serves /desktop-settings.html.
export default defineConfig({
  plugins: [react()],
  build: {
    outDir: "dist",
    emptyOutDir: false,
    rolldownOptions: {
      input: { "desktop-settings": fileURLToPath(new URL("desktop-settings.html", import.meta.url)) },
    },
  },
});
