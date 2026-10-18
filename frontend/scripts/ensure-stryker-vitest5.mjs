// @stryker-mutator/vitest-runner@10.0.0 joins suite and test names with a space
// when it builds test ids and the per-test name filter. Vitest 5 matches
// testNamePattern against the full name joined with " > " (see the Vitest 5
// migration guide). The filter matches nothing, so every covered mutant is
// reported Survived after running zero tests.
//
// The dry run still records coverage, and the runner returns that coverage
// even when coverageAnalysis is "off", so the planner keeps using the broken
// filter. https://github.com/stryker-mutator/stryker-js/issues/6210
//
// Two copies of collectTestName have to change together:
//   dist/src/test-helpers.js   — test ids and the mutant-run name filter
//   dist/src/stryker-setup.js  — coverage keys written during the dry run
// Patching only one of them makes every mutant NoCoverage.
//
// This file only understands 10.0.0, the version pinned in package.json.
// Any other installed version, or a 10.0.0 build whose collectTestName no
// longer matches, exits before Stryker starts. Drop the script when a
// published runner contains the fix (stryker-js #6214 / #6220).

import { readFileSync, writeFileSync } from 'node:fs';
import { createRequire } from 'node:module';
import path from 'node:path';

const SUPPORTED_VERSION = '10.0.0';
const broken = "return nameParts.join(' ').trim();";
const fixed = "return nameParts.filter((part) => part !== '').join(' > ').trim();";
const files = ['test-helpers.js', 'stryker-setup.js'];
const issue = 'https://github.com/stryker-mutator/stryker-js/issues/6210';

const require = createRequire(import.meta.url);
let packageJsonPath;
try {
  packageJsonPath = require.resolve('@stryker-mutator/vitest-runner/package.json');
} catch {
  fail(
    'Cannot find @stryker-mutator/vitest-runner. Install frontend dependencies before running mutation tests.',
  );
}

const installed = JSON.parse(readFileSync(packageJsonPath, 'utf8'));
if (installed.version !== SUPPORTED_VERSION) {
  fail(
    `@stryker-mutator/vitest-runner@${installed.version ?? 'unknown'} is installed, but the Vitest 5 workaround only matches ${SUPPORTED_VERSION}.`,
    'Mutation testing stops here. A different runner build reports covered mutants as survivors after zero tests, which shows up as a false 0% score.',
    `Pin @stryker-mutator/vitest-runner to ${SUPPORTED_VERSION}, or update scripts/ensure-stryker-vitest5.mjs after checking that the new version kills mutants.`,
    `Upstream: ${issue}`,
  );
}

const dist = path.join(path.dirname(packageJsonPath), 'dist', 'src');

for (const file of files) {
  const fullPath = path.join(dist, file);
  let source;
  try {
    source = readFileSync(fullPath, 'utf8');
  } catch (error) {
    fail(
      `Cannot read ${fullPath} (${error.code ?? error.message}).`,
      `@stryker-mutator/vitest-runner@${SUPPORTED_VERSION} was expected to contain collectTestName in this file.`,
      'Mutation testing stops here. Skipping the patch reports false 0% scores.',
      `Upstream: ${issue}`,
    );
  }

  const brokenCount = countOccurrences(source, broken);
  const fixedCount = countOccurrences(source, fixed);

  if (brokenCount === 1 && fixedCount === 0) {
    writeFileSync(fullPath, source.replace(broken, fixed));
    console.log(`Patched ${file} so Vitest 5 test names use " > ".`);
    continue;
  }

  if (brokenCount === 0 && fixedCount === 1) {
    console.log(`${file} already joins Vitest test names with " > ".`);
    continue;
  }

  fail(
    `${fullPath} does not contain the ${SUPPORTED_VERSION} collectTestName this workaround knows how to patch (space-joined copies: ${brokenCount}, Vitest 5 copies: ${fixedCount}).`,
    'Mutation testing stops here. Continuing would report false survivors.',
    'Update scripts/ensure-stryker-vitest5.mjs for this build of the runner.',
    `Upstream: ${issue}`,
  );
}

function countOccurrences(source, needle) {
  return source.split(needle).length - 1;
}

function fail(...lines) {
  console.error(lines.join('\n'));
  process.exit(1);
}
