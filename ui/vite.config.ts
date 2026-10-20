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
        // A self-signed TOFI (the default install) needs this to proxy over HTTPS.
        secure: !process.env.TOFI_DEV_API_INSECURE,
      },
      "/health": {
        target: process.env.TOFI_DEV_API_ORIGIN || "http://127.0.0.1:8321",
        changeOrigin: false,
        secure: !process.env.TOFI_DEV_API_INSECURE,
      },
    },
  },
  build: {
    outDir: "dist",
    emptyOutDir: true,
  },
});
