import { defineConfig } from "vite";
import react from "@vitejs/plugin-react";

// During development the SPA talks to the gateway via a proxy so the browser
// stays same-origin (no CORS). In production, serve the built assets behind the
// same host as the API, or set VITE_API_BASE.
export default defineConfig({
  plugins: [react()],
  server: {
    port: 5173,
    proxy: {
      "/v1": {
        target: "http://localhost:8080",
        changeOrigin: true,
      },
    },
  },
});
