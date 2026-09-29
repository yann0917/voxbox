import { defineConfig } from "vite";
import react from "@vitejs/plugin-react";
import tailwindcss from "@tailwindcss/vite";

export default defineConfig({
  plugins: [react(), tailwindcss()],
  // /api 代理默认指向本地 serve(8081)；VOXBOX_API 可指向其他端口（冒烟隔离实例等）
  server: {
    proxy: { "/api": { target: process.env.VOXBOX_API ?? "http://localhost:8081", ws: true } },
  },
});
