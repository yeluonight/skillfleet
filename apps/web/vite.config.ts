import path from "node:path"
import { defineConfig } from "vite"
import react from "@vitejs/plugin-react"
import tailwindcss from "@tailwindcss/vite"

// https://vite.dev/config/
export default defineConfig({
  plugins: [react(), tailwindcss()],
  resolve: {
    alias: {
      "@": path.resolve(__dirname, "./src"),
    },
  },
  server: {
    port: 5173,
    proxy: {
      // /api and /health hit the Go server during dev. The server's
      // default port is 47890 (config.yaml override). Cookies ride
      // through unchanged because both hosts are 127.0.0.1.
      "/api": {
        target: "http://127.0.0.1:47890",
        changeOrigin: false,
      },
      "/health": {
        target: "http://127.0.0.1:47890",
        changeOrigin: false,
      },
    },
  },
})
