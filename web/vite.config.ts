import tailwindcss from "@tailwindcss/vite";
import react from "@vitejs/plugin-react";
import { writeFile } from "node:fs/promises";
import { fileURLToPath, URL } from "node:url";
import type { Plugin } from "vite";
import { defineConfig } from "vitest/config";

const preserveGoEmbedTarget: Plugin = {
  name: "preserve-go-embed-target",
  apply: "build",
  async closeBundle() {
    const sentinel = fileURLToPath(new URL("../internal/webui/dist/.keep", import.meta.url));
    await writeFile(sentinel, "This file keeps the Go embed target present before the first Vite build.\n");
  },
};

export default defineConfig({
  base: "./",
  plugins: [preserveGoEmbedTarget, react(), tailwindcss()],
  resolve: {
    alias: {
      "@": fileURLToPath(new URL("./src", import.meta.url)),
    },
  },
  build: {
    outDir: "../internal/webui/dist",
    emptyOutDir: true,
    assetsDir: "assets",
  },
  server: {
    proxy: {
      "/api": {
        target: "http://127.0.0.1:8080",
        changeOrigin: true,
        ws: true,
      },
    },
  },
  test: {
    environment: "jsdom",
    setupFiles: "./src/test/setup.ts",
    clearMocks: true,
    restoreMocks: true,
  },
});
