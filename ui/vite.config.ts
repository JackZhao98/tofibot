import { defineConfig } from "vite";
import { readFileSync } from "node:fs";
import react from "@vitejs/plugin-react";
import { oauthAssetsPlugin } from "./oauthAssetsPlugin.ts";

export default defineConfig({
  plugins: [react(), oauthAssetsPlugin()],
  server: {
    port: 5173,
    // Serve the dev UI over HTTPS (for another machine on the LAN, where the Secure session cookie needs a secure origin).
    https: process.env.TOFI_DEV_HTTPS_CERT && process.env.TOFI_DEV_HTTPS_KEY
      ? { cert: readFileSync(process.env.TOFI_DEV_HTTPS_CERT), key: readFileSync(process.env.TOFI_DEV_HTTPS_KEY) }
      : undefined,
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
