import { readFileSync } from "node:fs";
import { defineConfig, type Plugin } from "vite";
import react from "@vitejs/plugin-react";

// serviceWorker emits sw.js with this build's ID and the files to precache:
// the bundle's hashed assets and the public icons and manifest.
function serviceWorker(): Plugin {
  return {
    name: "wada-service-worker",
    apply: "build",
    generateBundle(_, bundle) {
      const assets = Object.keys(bundle)
        .filter((f) => !f.endsWith(".map") && f !== "index.html")
        .map((f) => "/" + f);
      const precache = [
        ...assets,
        "/manifest.webmanifest",
        "/icon.svg",
        "/icon-192.png",
        "/icon-512.png",
        "/icon-maskable-512.png",
        "/apple-touch-icon.png",
      ];
      const build = Date.now().toString(36);
      const source = readFileSync(new URL("./sw/sw.js", import.meta.url), "utf8")
        .replace("__BUILD__", JSON.stringify(build))
        .replace("__PRECACHE__", JSON.stringify(precache));
      this.emitFile({ type: "asset", fileName: "sw.js", source });
    },
  };
}

export default defineConfig(({ mode }) => ({
  plugins: [react(), serviceWorker()],
  server: {
    port: 5173,
    host: true,
    // Tests read the Go wire declarations to check the client against them;
    // the dev server keeps serving only the web root.
    ...(mode === "test" ? { fs: { allow: [".", "../internal/protocol"] } } : {}),
  },
}));
