const path = require('node:path');
const tsParser = require(require.resolve('@typescript-eslint/parser', {
  paths: [path.join(__dirname, '../mobile')],
}));

/** @type {import('eslint').Linter.Config[]} */
module.exports = [
  {
    ignores: ['node_modules/**'],
  },
  {
    files: ['**/*.{js,jsx,ts,tsx}'],
    languageOptions: {
      parser: tsParser,
      parserOptions: {
        ecmaVersion: 'latest',
        sourceType: 'module',
        ecmaFeatures: { jsx: true },
      },
    },
    rules: {
      'no-restricted-imports': ['error', {
        patterns: [
          {
            regex: '(^|/)frontend(/|$)',
            message: '@stockbot/ui may only import @stockbot/shared from this repo.',
          },
          {
            regex: '(^|/)mobile(/|$)',
            message: '@stockbot/ui may only import @stockbot/shared from this repo.',
          },
        ],
      }],
    },
  },
];
