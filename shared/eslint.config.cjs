const path = require('node:path');

function resolveFromFrontendOrMobile(moduleName) {
  for (const pkg of ['../frontend', '../mobile']) {
    try {
      return require(require.resolve(moduleName, {
        paths: [path.join(__dirname, pkg)],
      }));
    } catch {
      // try the other app install tree
    }
  }
  throw new Error(
    `${moduleName} is not installed; run npm ci in frontend/ or mobile/.`,
  );
}

const tsParser = resolveFromFrontendOrMobile('@typescript-eslint/parser');

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
