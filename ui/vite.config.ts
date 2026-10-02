import { defineConfig } from "vite";
import react from "@vitejs/plugin-react";
import { oauthAssetsPlugin } from "./oauthAssetsPlugin.ts";

export default defineConfig({
  plugins: [react(), oauthAssetsPlugin()],
  server: {
    port: 5173,
    proxy: {
      "/api": {
        target: process.env.TOFI_DEV_API_ORIGIN || "http://127.0.0.1:8321",
        changeOrigin: false,
      },
      "/health": {
        target: process.env.TOFI_DEV_API_ORIGIN || "http://127.0.0.1:8321",
        changeOrigin: false,
      },
    },
  },
  build: {
    outDir: "dist",
    emptyOutDir: true,
  },
});
