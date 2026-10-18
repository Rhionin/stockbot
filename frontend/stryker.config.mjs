// Mutation testing for the Vite/Vitest frontend.
// `npm run test:mutation` patches @stryker-mutator/vitest-runner so Vitest 5
// test names match. Running `stryker` directly on Vitest 5 reports false
// survivors. See scripts/ensure-stryker-vitest5.mjs.

/** @type {import('@stryker-mutator/api/core').StrykerOptions} */
const config = {
  testRunner: 'vitest',
  vitest: {
    configFile: 'vite.config.ts',
    // Unit tests import the module they cover, so related mode is valid.
    related: true,
  },
  mutate: [
    'src/**/*.ts',
    'src/**/*.tsx',
    '!src/**/*.test.ts',
    '!src/**/*.test.tsx',
    '!src/test-setup.ts',
  ],
  // The Vitest runner records per-test coverage on the dry run and the planner
  // uses it whenever that coverage is present, including when this is "off".
  // Per-test is the mode the Vitest 5 name patch makes accurate.
  coverageAnalysis: 'perTest',
  disableTypeChecks: true,
  reporters: process.env.MUTATION_CANARY_REPORTERS
    ? process.env.MUTATION_CANARY_REPORTERS.split(',').map((name) => name.trim()).filter(Boolean)
    : ['clear-text', 'progress', 'html', 'json'],
  htmlReporter: {
    fileName: 'reports/mutation/index.html',
  },
  jsonReporter: {
    fileName: process.env.MUTATION_CANARY_JSON ?? 'reports/mutation/mutation-report.json',
  },
  tempDirName: '.stryker-tmp',
  cleanTempDir: 'always',
  thresholds: {
    high: 80,
    low: 60,
    break: null,
  },
};

export default config;
