/// <reference types="vitest/config" />
import { dirname, resolve } from 'node:path'
import { fileURLToPath } from 'node:url'
import { defineConfig } from 'vite'
import react from '@vitejs/plugin-react'

const root = dirname(fileURLToPath(import.meta.url))
const shared = resolve(root, '../shared')

// https://vite.dev/config/
export default defineConfig({
  plugins: [react()],
  resolve: {
    alias: {
      '@stockbot/shared': shared,
    },
  },
  server: {
    fs: {
      allow: [resolve(root, '..')],
    },
    proxy: {
      '/api': {
        target: process.env.VITE_API_PROXY_TARGET ?? 'http://127.0.0.1:8080',
      },
      '/brand': {
        target: process.env.VITE_API_PROXY_TARGET ?? 'http://127.0.0.1:8080',
      },
    },
  },
  test: {
    include: ['./src/**/*.test.{ts,tsx}', '../shared/**/*.test.{ts,tsx}'],
    environment: 'jsdom',
    setupFiles: ['./src/test-setup.ts'],
  },
})
