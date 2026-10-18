// Proves the Vitest 5 workaround is in effect. The || to && mutant on line 13
// of src/utils/inventoryFilter.ts is killed by the existing unit tests. An
// unpatched @stryker-mutator/vitest-runner@10.0.0 reports that same mutant as
// Survived after running zero tests.
// https://github.com/stryker-mutator/stryker-js/issues/6210

import { spawnSync } from 'node:child_process';
import { readFileSync } from 'node:fs';
import path from 'node:path';
import { fileURLToPath } from 'node:url';

const root = path.resolve(path.dirname(fileURLToPath(import.meta.url)), '..');
const reportPath = path.join(root, 'reports', 'mutation', 'canary.json');
const sourceFile = 'src/utils/inventoryFilter.ts';
const line = 13;

export function findCanaryMutant(report) {
  const mutants = report?.files?.[sourceFile]?.mutants ?? [];
  return mutants.find((mutant) =>
    mutant.mutatorName === 'LogicalOperator'
    && mutant.location?.start?.line === line
    && typeof mutant.replacement === 'string'
    && mutant.replacement.includes('&&')
    && mutant.replacement.includes('productName.includes'),
  );
}

export function canaryProblem(report) {
  const mutant = findCanaryMutant(report);
  if (!mutant) {
    return `Mutation canary could not find the ${sourceFile}:${line} LogicalOperator mutant that replaces || with &&. The score is not evidence that mutants reach the tests.`;
  }
  const completed = Number(mutant.testsCompleted ?? 0);
  if (mutant.status !== 'Killed' || completed < 1) {
    return `Mutation canary failed: ${sourceFile}:${line} || replaced with && is ${mutant.status ?? 'missing'} after ${completed} test(s). The existing unit tests kill this mutant when it actually runs. A survivor here means Stryker never executed the mutated code, so the score is not valid.`;
  }
  return null;
}

function runCanary() {
  const result = spawnSync(
    path.join(root, 'node_modules', '.bin', 'stryker'),
    ['run', '--mutate', sourceFile],
    {
      cwd: root,
      stdio: 'inherit',
      env: {
        ...process.env,
        MUTATION_CANARY_REPORTERS: 'json',
        MUTATION_CANARY_JSON: 'reports/mutation/canary.json',
      },
    },
  );

  if (result.error) {
    console.error(`Could not start Stryker for the mutation canary: ${result.error.message}`);
    process.exit(1);
  }
  if (result.status !== 0) {
    console.error(`Stryker exited ${result.status ?? 'without a status'} during the mutation canary. The line ${line} mutant was not checked.`);
    process.exit(result.status ?? 1);
  }

  let report;
  try {
    report = JSON.parse(readFileSync(reportPath, 'utf8'));
  } catch (error) {
    console.error(`Mutation canary could not read ${reportPath} (${error.message}).`);
    process.exit(1);
  }

  const problem = canaryProblem(report);
  if (problem) {
    console.error(problem);
    console.error(`Report: ${reportPath}`);
    process.exit(1);
  }

  const mutant = findCanaryMutant(report);
  console.log(
    `Canary passed: ${sourceFile}:${line} ${mutant.mutatorName} (|| replaced with &&) is Killed after ${mutant.testsCompleted} test(s).`,
  );
}

const invokedDirectly = process.argv[1]
  && path.resolve(process.argv[1]) === fileURLToPath(import.meta.url);
if (invokedDirectly) {
  runCanary();
}
