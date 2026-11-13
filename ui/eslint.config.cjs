const path = require('node:path');

function resolveFromFrontendOrMobile(moduleName) {
  for (const pkg of ['../mobile', '../frontend']) {
    try {
      return require(require.resolve(moduleName, {
        paths: [path.join(__dirname, pkg)],
      }));
    } catch {
      // try the other app install tree
    }
  }
  throw new Error(
    `${moduleName} is not installed; run npm ci in mobile/ or frontend/.`,
  );
}

const tsParser = resolveFromFrontendOrMobile('@typescript-eslint/parser');

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
