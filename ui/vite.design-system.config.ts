import { fileURLToPath } from "node:url";
import { defineConfig } from "vite";
import react from "@vitejs/plugin-react";

// A separate build keeps the workspace graph and its CSS unchanged.
export default defineConfig({
  plugins: [react(), {
    name: "design-system-html",
    enforce: "post",
    generateBundle: { order: "post", handler(_, bundle) {
      const html = bundle["catalog.html"];
      if (!html) throw new Error("Missing catalog.html");
      if (html.type !== "asset") throw new Error("Expected an HTML asset");
      this.emitFile({ type: "asset", fileName: "design-system.html", source: html.source });
      delete bundle["catalog.html"];
    } },
  }],
  build: {
    outDir: "dist",
    emptyOutDir: false,
    rolldownOptions: {
      input: { "design-system": fileURLToPath(new URL("catalog.html", import.meta.url)) },
    },
  },
});
