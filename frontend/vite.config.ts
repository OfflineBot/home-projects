import { defineConfig } from "vite";
import react from "@vitejs/plugin-react";

// In development the API runs on :5000 and Vite on :5173. In production nginx
// serves the built files and forwards /api, /git and /s to the backend.
export default defineConfig({
  plugins: [react()],
  server: {
    port: 5173,
    proxy: {
      "/api": { target: "http://127.0.0.1:5000", changeOrigin: true },
      "/git": { target: "http://127.0.0.1:5000", changeOrigin: true },
      "/s": { target: "http://127.0.0.1:5000", changeOrigin: true },
      "/health": { target: "http://127.0.0.1:5000", changeOrigin: true },
    },
  },
  build: { outDir: "dist", sourcemap: false },
});
