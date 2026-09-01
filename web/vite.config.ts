import { defineConfig } from 'vitest/config'
import react from '@vitejs/plugin-react'

const apiTarget =
  process.env.SING_BOX_WEBUI_DEV_API ?? 'http://127.0.0.1:31334'
const devPort = Number(process.env.SING_BOX_WEBUI_DEV_PORT ?? '31333')

export default defineConfig({
  plugins: [react()],
  server: {
    host: '127.0.0.1',
    port: devPort,
    strictPort: true,
    proxy: {
      '/api': {
        target: apiTarget,
        changeOrigin: true,
      },
      '/healthz': {
        target: apiTarget,
        changeOrigin: true,
      },
    },
  },
  preview: {
    host: '127.0.0.1',
    port: 4173,
    strictPort: true,
  },
  test: {
    environment: 'jsdom',
    include: ['src/**/*.test.{ts,tsx}'],
    setupFiles: './src/test/setup.ts',
  },
})
