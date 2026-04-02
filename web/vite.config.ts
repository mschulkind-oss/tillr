import { defineConfig } from 'vite'
import react from '@vitejs/plugin-react'
import tailwindcss from '@tailwindcss/vite'

const backendPort = process.env.TILLR_PORT || '3847'
const vitePort = parseInt(process.env.VITE_PORT || '5173', 10)

export default defineConfig({
  plugins: [react(), tailwindcss()],
  server: {
    port: vitePort,
    proxy: {
      '/api': `http://localhost:${backendPort}`,
      '/ws': {
        target: `ws://localhost:${backendPort}`,
        ws: true,
      },
    },
  },
  build: {
    outDir: '../internal/server/assets/dist',
    emptyOutDir: true,
  },
})
