/// <reference types="vitest/config" />
import { dirname, resolve } from 'node:path'
import { fileURLToPath } from 'node:url'
import { createTamaguiPlugins } from '@tamagui/vite-plugin'
import { defineConfig } from 'vite'
import react from '@vitejs/plugin-react'

const root = dirname(fileURLToPath(import.meta.url))
const shared = resolve(root, '../shared')
const ui = resolve(root, '../ui/src')
const { plugins: tamaguiVitePlugins } = createTamaguiPlugins()
// https://vite.dev/config/
export default defineConfig(({ mode }) => ({
  plugins: mode === 'test' ? [react()] : [react(), ...tamaguiVitePlugins],
  define: {
    'process.env.NODE_ENV': JSON.stringify(mode === 'production' ? 'production' : 'development'),
    __DEV__: mode !== 'production',
  },
  resolve: {
    alias: {
      '@stockbot/shared': shared,
      '@stockbot/ui': ui,
      'react-native': 'react-native-web',
      react: resolve(root, 'node_modules/react'),
      'react/jsx-runtime': resolve(root, 'node_modules/react/jsx-runtime'),
      'react/jsx-dev-runtime': resolve(root, 'node_modules/react/jsx-dev-runtime'),
      'react-dom': resolve(root, 'node_modules/react-dom'),
      tamagui: resolve(root, 'node_modules/tamagui'),
    },
  },
  optimizeDeps: {
    include: ['tamagui', 'react-native-web', 'react', 'react-dom'],
  },
  ssr: {
    noExternal: ['react', 'react-dom', 'tamagui', /@tamagui\//],
  },
  build: {
    rollupOptions: {
      input: {
        main: resolve(root, 'index.html'),
      },
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
    include: ['./src/**/*.test.{ts,tsx}', '../shared/**/*.test.{ts,tsx}', '../ui/**/*.test.{ts,tsx}'],
    environment: 'jsdom',
    setupFiles: ['./src/test-setup.ts', './src/tamagui-spike/vitest-tamagui-setup.ts'],
    coverage: {
      allowExternal: true,
      include: ['src/**/*.{ts,tsx}', '../shared/**/*.{ts,tsx}', '../ui/**/*.{ts,tsx}'],
      exclude: ['../shared/**/*.test.{ts,tsx}', '../ui/**/*.test.{ts,tsx}'],
    },
  },
}))
