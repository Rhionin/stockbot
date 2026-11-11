const path = require('node:path');
const tsParser = require(require.resolve('@typescript-eslint/parser', {
  paths: [path.join(__dirname, '../mobile')],
}));

/** @type {import('eslint').Linter.Config[]} */
module.exports = [
  {
    ignores: ['**/*.test.ts', '**/*.test.tsx'],
  },
  {
    files: ['**/*.{js,jsx,ts,tsx}'],
    languageOptions: {
      parser: tsParser,
      parserOptions: {
        ecmaVersion: 'latest',
        sourceType: 'module',
      },
    },
    rules: {
      'no-restricted-imports': ['error', {
        patterns: [
          {
            regex: '(^|/)frontend(/|$)',
            message: 'shared/ is a leaf and must not import app trees.',
          },
          {
            regex: '(^|/)mobile(/|$)',
            message: 'shared/ is a leaf and must not import app trees.',
          },
          {
            regex: '(^|/)ui(\\/|$)',
            message: 'shared/ must not import @stockbot/ui (use @stockbot/shared only).',
          },
          {
            regex: '\\.\\./ui(/|$)',
            message: 'shared/ must not import ui/ via a relative path.',
          },
        ],
      }],
    },
  },
];
