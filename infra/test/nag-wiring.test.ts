import assert from 'node:assert/strict';
import { mkdtempSync, readFileSync, rmSync } from 'node:fs';
import { tmpdir } from 'node:os';
import path from 'node:path';
import { describe, it } from 'node:test';
import { Bucket } from 'aws-cdk-lib/aws-s3';
import { Stack } from 'aws-cdk-lib/core';
import { createApp } from '../lib/create-app.js';
import { STAGE_NAMES } from '../lib/stage-env.js';

const cdkContext = JSON.parse(readFileSync(new URL('../cdk.json', import.meta.url), 'utf8'))
  .context as Record<string, unknown>;

const ACCOUNT_ENV = ['STOCKBOT_DEV_ACCOUNT', 'STOCKBOT_PROD_ACCOUNT'] as const;

type ValidationReport = {
  pluginReports: Array<{
    pluginName?: string;
    violations?: Array<{
      ruleName: string;
      violatingConstructs?: Array<{ constructPath?: string }>;
    }>;
  }>;
};

describe('cdk-nag AwsSolutions', () => {
  for (const stage of STAGE_NAMES) {
    it(`fails synth when ${stage} has an S3 bucket with no access logs`, () => {
      const savedEnv = ACCOUNT_ENV.map((key) => [key, process.env[key]] as const);
      for (const key of ACCOUNT_ENV) {
        delete process.env[key];
      }
      const outdir = mkdtempSync(path.join(tmpdir(), 'stockbot-nag-'));
      try {
        const app = createApp({
          outdir,
          context: { ...cdkContext, devAccount: '', prodAccount: '' },
        });
        const data = app.node.findChild(stage).node.findChild('Data');
        assert.ok(Stack.isStack(data), `${stage}/Data is not a stack`);
        new Bucket(data, 'NoLogs');
        assert.throws(() => app.synth(), /Validation failed/);

        const report = JSON.parse(
          readFileSync(path.join(outdir, 'validation-report.json'), 'utf8'),
        ) as ValidationReport;
        const nag = report.pluginReports.filter((pluginReport) =>
          (pluginReport.pluginName ?? '').includes('AwsSolutions'),
        );
        const bucketPath = `${stage}/Data/NoLogs`;
        const onThisBucket = nag.flatMap((pluginReport) =>
          (pluginReport.violations ?? []).filter(
            (violation) =>
              violation.ruleName.includes('AwsSolutions-S1') &&
              (violation.violatingConstructs ?? []).some((construct) =>
                (construct.constructPath ?? '').includes(bucketPath),
              ),
          ),
        );
        assert.ok(
          onThisBucket.length > 0,
          `${bucketPath} did not produce AwsSolutions-S1: ${JSON.stringify(nag)}`,
        );
      } finally {
        rmSync(outdir, { recursive: true, force: true });
        for (const [key, value] of savedEnv) {
          if (value === undefined) {
            delete process.env[key];
          } else {
            process.env[key] = value;
          }
        }
      }
    });
  }
});
