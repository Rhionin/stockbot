import js from '@eslint/js'
import globals from 'globals'
import reactHooks from 'eslint-plugin-react-hooks'
import reactRefresh from 'eslint-plugin-react-refresh'
import tseslint from 'typescript-eslint'
import { defineConfig, globalIgnores } from 'eslint/config'

// ESLint's base path is the directory of the config file it loads. shared/
// is outside this package, so repo-root eslint.config.mjs loads this export
// with the repo as the base path. Relative imports inside shared/ do not
// match the frontend or mobile segments.
export const sharedLintConfig = defineConfig([
  {
    files: ['**/*.{ts,tsx}'],
    extends: [
      js.configs.recommended,
      tseslint.configs.recommended,
      reactHooks.configs.flat.recommended,
      reactRefresh.configs.vite,
    ],
    languageOptions: {
      globals: globals.browser,
    },
    rules: {
      'no-restricted-imports': ['error', {
        patterns: [
          {
            regex: '(^|/)frontend(/|$)',
            message: 'Shared code cannot import the web app.',
          },
          {
            regex: '(^|/)mobile(/|$)',
            message: 'Shared code cannot import the phone app.',
          },
        ],
      }],
    },
  },
])

export default defineConfig([
  globalIgnores(['dist']),
  {
    files: ['**/*.{ts,tsx}'],
    extends: [
      js.configs.recommended,
      tseslint.configs.recommended,
      reactHooks.configs.flat.recommended,
      reactRefresh.configs.vite,
    ],
    languageOptions: {
      globals: globals.browser,
    },
    rules: {
      'no-restricted-imports': ['error', {
        patterns: [{
          regex: '(^|/)mobile(/|$)',
          message: 'The web app cannot import the phone app. Shared code lives in @stockbot/shared.',
        }],
      }],
    },
  },
])
