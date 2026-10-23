import type { App, Environment } from 'aws-cdk-lib/core';

/** Every hosted stack is in this region. Design §8 (us-east-1 for ACM and the rest). */
export const REGION = 'us-east-1';

export const STAGE_NAMES = ['dev', 'prod'] as const;

export type StageName = (typeof STAGE_NAMES)[number];

const ACCOUNT_ID = /^[0-9]{12}$/;

const ENV_VAR: Record<StageName, string> = {
  dev: 'STOCKBOT_DEV_ACCOUNT',
  prod: 'STOCKBOT_PROD_ACCOUNT',
};

export function contextKey(stage: StageName): string {
  return `${stage}Account`;
}

/**
 * Account IDs are not known yet and are never hardcoded.
 * Set `devAccount` / `prodAccount` in cdk.json context, or `STOCKBOT_DEV_ACCOUNT`
 * / `STOCKBOT_PROD_ACCOUNT`. A blank value means unset, so `cdk synth` still runs.
 * When both are set, the environment variable wins so a shell can override the
 * committed context. Dev and prod may be two accounts or the same account.
 */
export function resolveStageEnv(app: App, stage: StageName): Environment {
  const account = resolveAccount(app, stage);
  if (account === undefined) {
    return { region: REGION };
  }
  return { account, region: REGION };
}

function resolveAccount(app: App, stage: StageName): string | undefined {
  const envVar = ENV_VAR[stage];
  const fromEnv = readAccount(stage, process.env[envVar], envVar);
  if (fromEnv !== undefined) {
    return fromEnv;
  }
  return readAccount(
    stage,
    app.node.tryGetContext(contextKey(stage)),
    `context ${contextKey(stage)}`,
  );
}

function readAccount(stage: StageName, raw: unknown, source: string): string | undefined {
  if (raw === undefined || raw === '') {
    return undefined;
  }
  if (typeof raw !== 'string') {
    throw new Error(
      `The ${stage} account from ${source} must be a 12-digit AWS account ID string.`,
    );
  }
  const account = raw.trim();
  if (account === '') {
    return undefined;
  }
  if (!ACCOUNT_ID.test(account)) {
    throw new Error(
      `The ${stage} account from ${source} must be a 12-digit AWS account ID. Received "${account}".`,
    );
  }
  return account;
}
