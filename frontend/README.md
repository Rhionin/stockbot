# Stockbot Frontend Development Guide

This directory contains the React frontend for Stockbot, built with TypeScript and Vite.

## Requirements

Node.js 24 or newer. `.nvmrc` pins `24` (run `nvm use` in this directory). Vitest crashes under Node 20 (jsdom/undici). `package.json` sets `"engines": { "node": ">=24" }` and `.npmrc` sets `engine-strict=true`, so `npm ci` on an older Node fails immediately.

## Local Development Workflow

Stockbot runs as two separate processes during development:

1. **Vite dev server** - serves the React frontend with hot module replacement
2. **Go API server** - serves the `/api` routes and embedded assets

### Starting Development

1. **Start the Go API server** (from the repository root):
   ```bash
   go run ./cmd/server
   ```
   This starts the server on the address specified by the `ADDR` environment variable (default: `:8080`).

2. **Start the Vite dev server** (from the `frontend/` directory):
   ```bash
   npm run dev
   ```
   This starts the Vite dev server, typically on `http://localhost:5173`.

3. **Browse to the Vite dev server URL** for development. The frontend will have hot module replacement and all the modern development features.

### API Proxy Configuration

The Vite dev server is configured in `vite.config.ts` to proxy `/api` requests to the Go server:

```typescript
server: {
  proxy: {
    '/api': {
      target: process.env.VITE_API_PROXY_TARGET ?? 'http://127.0.0.1:8080',
    },
  },
}
```

This means that when the frontend makes same-origin requests to `/api/products`, `/api/scans`, etc., Vite forwards them to the Go server. This proxy configuration is why the frontend can use the same API paths in both development and production without environment-specific base URLs.

### Development vs Production Serving

- **Development**: Browse the Vite dev server port (usually `:5173`) to get the live UI with hot module replacement
- **Production**: The Go server serves the compiled frontend assets embedded in the binary

**Important**: Moving between dev-server serving and embedded serving requires no build step and no build flag. If you browse directly to the Go server's address during development (e.g., `http://localhost:8080`), you'll see the placeholder assets document rather than the live UI. This is expected behavior - the placeholder identifies itself so you know you're not looking at the development version.

## Available Scripts

### Development
- `npm run dev` - Start the Vite development server with hot module replacement
- `npm run preview` - Preview the production build locally

### Building
- `npm run build` - Build the frontend for production (runs TypeScript compilation and Vite build)

### Code Quality
- `npm run lint` - Run ESLint on all source files
- `npm test` - Run unit tests with Vitest in watch mode
- `npm run e2e` - Run end-to-end tests with Playwright

### Testing Approach

The frontend uses a multi-layered testing strategy:

1. **Unit Tests** (Vitest + React Testing Library)
   - Located alongside source files with `.test.tsx` extension
   - Test individual components and utility functions
   - Run with `npm test` (watch mode) or `npm test run` (single run)

2. **End-to-End Tests** (Playwright)
   - Located in the `e2e/` directory
   - Test complete user workflows across the full application
   - Run with `npm run e2e`

3. **Type Checking**
   - TypeScript provides compile-time type safety
   - Run explicitly with `npx tsc -b` or as part of the build process

The test configuration includes:
- **jsdom** environment for unit tests (configured in `vite.config.ts`)
- **Setup file** at `./src/test-setup.ts` for test utilities and global configuration
- **@testing-library/jest-dom** for additional DOM matchers

## Project Structure

- `src/` - Source code
  - `components/` - React components organized by feature
  - `api/` - API client and types
  - `assets/` - Static assets
- `e2e/` - End-to-end tests
- `public/` - Static public assets
- `dist/` - Build output (created by `npm run build`)

## Technology Stack

- **React 19** with TypeScript
- **Vite** for fast development and building
- **Mantine** for UI components
- **React Router** for client-side routing
- **Vitest** for unit testing
- **Playwright** for end-to-end testing
- **ESLint** for code linting
