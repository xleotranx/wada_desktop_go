import { defineConfig } from "vite";
import react from "@vitejs/plugin-react";

export default defineConfig(({ mode }) => ({
  plugins: [react()],
  server: {
    port: 5173,
    host: true,
    // Tests read the Go wire declarations to check the client against them;
    // the dev server keeps serving only the web root.
    ...(mode === "test" ? { fs: { allow: [".", "../internal/protocol"] } } : {}),
  },
}));
