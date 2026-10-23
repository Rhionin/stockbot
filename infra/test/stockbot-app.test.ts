import assert from 'node:assert/strict';
import { mkdtempSync, readFileSync, rmSync } from 'node:fs';
import { tmpdir } from 'node:os';
import path from 'node:path';
import { afterEach, describe, it } from 'node:test';
import { App } from 'aws-cdk-lib/core';
import { createApp } from '../lib/create-app.js';
import { REGION, resolveStageEnv } from '../lib/stage-env.js';

const cdkContext = JSON.parse(readFileSync(new URL('../cdk.json', import.meta.url), 'utf8'))
  .context as Record<string, unknown>;

const ACCOUNT_ENV = ['STOCKBOT_DEV_ACCOUNT', 'STOCKBOT_PROD_ACCOUNT'] as const;

const originalEnv = new Map<string, string | undefined>(
  ACCOUNT_ENV.map((key) => [key, process.env[key]]),
);

afterEach(() => {
  for (const key of ACCOUNT_ENV) {
    const value = originalEnv.get(key);
    if (value === undefined) {
      delete process.env[key];
    } else {
      process.env[key] = value;
    }
  }
});

function clearAccountEnv(): void {
  for (const key of ACCOUNT_ENV) {
    delete process.env[key];
  }
}

describe('resolveStageEnv', () => {
  it('uses us-east-1 and leaves the account unset when nothing is configured', () => {
    clearAccountEnv();
    const env = resolveStageEnv(new App(), 'dev');
    assert.equal(env.region, REGION);
    assert.equal(env.account, undefined);
  });

  it('reads a 12-digit account from context', () => {
    clearAccountEnv();
    const app = new App({ context: { devAccount: '123456789012', prodAccount: '' } });
    assert.equal(resolveStageEnv(app, 'dev').account, '123456789012');
    assert.equal(resolveStageEnv(app, 'prod').account, undefined);
  });

  it('lets the environment variable override context', () => {
    clearAccountEnv();
    process.env.STOCKBOT_DEV_ACCOUNT = '222222222222';
    const app = new App({ context: { devAccount: '111111111111' } });
    assert.equal(resolveStageEnv(app, 'dev').account, '222222222222');
  });

  it('trims a blank environment variable and falls through to context', () => {
    clearAccountEnv();
    process.env.STOCKBOT_PROD_ACCOUNT = '   ';
    const app = new App({ context: { prodAccount: ' 333333333333 ' } });
    assert.equal(resolveStageEnv(app, 'prod').account, '333333333333');
  });

  it('rejects an account that is not 12 digits', () => {
    clearAccountEnv();
    const app = new App({ context: { devAccount: '1234' } });
    assert.throws(() => resolveStageEnv(app, 'dev'), /12-digit AWS account ID/);
  });

  it('rejects a non-string context value', () => {
    clearAccountEnv();
    const app = new App({ context: { prodAccount: 123456789012 } });
    assert.throws(() => resolveStageEnv(app, 'prod'), /must be a 12-digit AWS account ID string/);
  });
});

describe('createApp', () => {
  it('synthesizes empty dev and prod stacks in us-east-1 with the common tags', () => {
    clearAccountEnv();
    const outdir = mkdtempSync(path.join(tmpdir(), 'stockbot-cdk-'));
    try {
      const assembly = createApp({
        outdir,
        context: { ...cdkContext, devAccount: '', prodAccount: '' },
      }).synth();
      const stacks = assembly.stacksRecursively.map((stack) => {
        const template = JSON.parse(readFileSync(stack.templateFullPath, 'utf8')) as {
          Resources?: Record<string, { Type: string }>;
        };
        return {
          name: stack.stackName,
          account: stack.environment.account,
          region: stack.environment.region,
          tags: stack.tags,
          resourceTypes: Object.values(template.Resources ?? {}).map((resource) => resource.Type),
        };
      });

      assert.deepEqual(stacks.map((stack) => stack.name).sort(), [
        'dev-App',
        'dev-Data',
        'dev-Edge',
        'dev-Identity',
        'dev-Ops',
        'prod-App',
        'prod-Data',
        'prod-Edge',
        'prod-Identity',
        'prod-Ops',
      ]);

      for (const stack of stacks) {
        const stage = stack.name.startsWith('prod-') ? 'prod' : 'dev';
        assert.equal(stack.region, 'us-east-1');
        assert.equal(stack.account, 'unknown-account');
        assert.deepEqual(stack.tags, {
          app: 'stockbot',
          owner: 'network-engineer',
          stage,
        });
        assert.deepEqual(
          stack.resourceTypes.filter((type) => type !== 'AWS::CDK::Metadata'),
          [],
        );
      }

      const report = JSON.parse(
        readFileSync(path.join(outdir, 'validation-report.json'), 'utf8'),
      ) as {
        pluginReports: Array<{
          violations?: unknown[];
          suppressedViolations?: Array<{ reason?: string }>;
        }>;
      };
      const violations = report.pluginReports.flatMap(
        (pluginReport) => pluginReport.violations ?? [],
      );
      const suppressed = report.pluginReports.flatMap(
        (pluginReport) => pluginReport.suppressedViolations ?? [],
      );
      assert.deepEqual(violations, []);
      assert.equal(suppressed.length, 10);
      for (const finding of suppressed) {
        assert.match(finding.reason ?? '', /Skeleton stack with no resources yet/);
      }
    } finally {
      rmSync(outdir, { recursive: true, force: true });
    }
  });

  it('pins both stages to the accounts from context', () => {
    clearAccountEnv();
    const outdir = mkdtempSync(path.join(tmpdir(), 'stockbot-cdk-'));
    try {
      const assembly = createApp({
        outdir,
        context: {
          ...cdkContext,
          devAccount: '111111111111',
          prodAccount: '222222222222',
        },
      }).synth();
      const accounts = Object.fromEntries(
        assembly.stacksRecursively.map((stack) => [stack.stackName, stack.environment.account]),
      );
      assert.equal(accounts['dev-Edge'], '111111111111');
      assert.equal(accounts['prod-Ops'], '222222222222');
    } finally {
      rmSync(outdir, { recursive: true, force: true });
    }
  });
});
