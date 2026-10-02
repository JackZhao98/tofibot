import { fileURLToPath } from "node:url";
import { defineConfig } from "vite";
import react from "@vitejs/plugin-react";

// The Motion Lab showcase page builds after the app, into the same dist/, as a
// separate bundle so the app's own chunks and file names stay exactly as they were.
// In development the main dev server already serves /motion-lab.html.
export default defineConfig({
  plugins: [react()],
  build: {
    outDir: "dist",
    emptyOutDir: false,
    rolldownOptions: {
      input: { "motion-lab": fileURLToPath(new URL("motion-lab.html", import.meta.url)) },
    },
  },
});
