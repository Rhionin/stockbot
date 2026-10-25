# Hosted Stockbot infrastructure

TypeScript CDK app for the hosted multi-household service. Stack boundaries follow §8 of the design in [PR #224](https://github.com/Rhionin/stockbot/pull/224) (`docs/design/multi-tenant-saas.md`). This directory is the skeleton from [issue #339](https://github.com/Rhionin/stockbot/issues/339): the stacks exist, and the resources inside them do not yet.

`cdk synth` does not need AWS credentials. Bootstrap and deploy wait until the accounts exist.

## Account, stage, and region

| Stage  | Account                                           | Region      | Stacks                         |
| ------ | ------------------------------------------------- | ----------- | ------------------------------ |
| `dev`  | context `devAccount`, or `STOCKBOT_DEV_ACCOUNT`   | `us-west-2` | Edge, Data, Identity, App, Ops |
| `prod` | context `prodAccount`, or `STOCKBOT_PROD_ACCOUNT` | `us-west-2` | Edge, Data, Identity, App, Ops |

Both stages use one region constant, `REGION` in `lib/stage-env.ts` (`us-west-2`, Oregon). Change that constant to move dev and prod together, then update the bootstrap commands below to match.

Account IDs are not in source. `cdk.json` ships both context keys as empty strings, which means "not set". Dev and prod may be two accounts or the same account (separate accounts when that is possible).

CloudFront public certificates are issued in `us-east-1` even when these stacks run in `us-west-2`. The Edge stack does not create that certificate yet, so there is no second region constant.

When both a context value and the environment variable are set, the environment variable wins.

CloudFormation stack names are `<stage>-<Stack>`, for example `dev-Edge`. The CDK selectors are `dev/Edge` and `prod/Edge`.

Every stack is tagged `app=stockbot`, `stage=dev` or `stage=prod`, and `owner=network-engineer`.

cdk-nag's AwsSolutions pack is registered on every stage and runs during synth. A suppression has to carry a written reason. Each empty stack acknowledges CloudFormation rule `F0001` (a template must contain resources) because these stacks are still skeletons. That acknowledgment is removed when the stack gains resources.

| Stack    | Will hold (not in this skeleton)                     |
| -------- | ---------------------------------------------------- |
| Edge     | CloudFront, WAF, DNS                                 |
| Data     | Aurora DSQL, backups, exports bucket, KMS            |
| Identity | Cognito, SES                                         |
| App      | Lambda API, jobs, migrate, AppSync Events, Scheduler |
| Ops      | Logs, alarms, SNS, budgets                           |

## Prerequisites

Node.js 24 (`infra/.nvmrc`). Install dependencies from this directory:

```bash
cd infra
npm ci
```

## Synth

```bash
npx cdk synth 'dev/*' 'prod/*' --strict
```

With no account ids set, the templates are region-pinned to `us-west-2` and account-agnostic. Set the accounts before deploy.

## Bootstrap

Bootstrap is once per account in `us-west-2`. Run it for each stage. If dev and prod share an account, the second command hits the same account and region, so one successful bootstrap covers both.

```bash
npx cdk bootstrap aws://<dev-account>/us-west-2
npx cdk bootstrap aws://<prod-account>/us-west-2
```

Replace `<dev-account>` and `<prod-account>` with the 12-digit ids. Export them first if they are not already in `cdk.json`:

```bash
export STOCKBOT_DEV_ACCOUNT=<dev-account>
export STOCKBOT_PROD_ACCOUNT=<prod-account>
```

## Deploy dev

After the dev account is bootstrapped:

```bash
export STOCKBOT_DEV_ACCOUNT=<dev-account>
npx cdk deploy 'dev/*' --strict
```

That deploys the empty `dev-Edge`, `dev-Data`, `dev-Identity`, `dev-App`, and `dev-Ops` stacks. Prod is the same shape with `prod/*` and `STOCKBOT_PROD_ACCOUNT`. Do that only after dev is reviewed.

## CI

`.github/workflows/infra.yml` runs on pull requests (and pushes to `master`) that touch `infra/` or that workflow. It installs dependencies, lints, typechecks, and synthesizes both stages. It does not use AWS credentials and it does not deploy. The Pi image build and deploy hook stay in `.github/workflows/ci.yml`.
