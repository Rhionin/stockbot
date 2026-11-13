const { defineConfig } = require('eslint/config');
const { fixupConfigRules } = require('@eslint/compat');
const expoConfig = require('eslint-config-expo/flat');

module.exports = defineConfig([
  ...fixupConfigRules(expoConfig),
  {
    ignores: ['dist/**', 'coverage/**', '.expo/**', 'scripts/**'],
  },
  {
    settings: {
      'import/resolver': {
        typescript: {
          project: './tsconfig.json',
        },
      },
    },
    files: ['**/*.{js,jsx,ts,tsx}'],
    rules: {
      'no-restricted-imports': ['error', {
        patterns: [{
          regex: '(^|/)frontend(/|$)',
          message: 'The phone app cannot import the web app. Shared code lives in @stockbot/shared.',
        }],
      }],
    },
  },
]);
