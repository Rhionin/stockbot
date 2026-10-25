import { Stack, type StackProps } from 'aws-cdk-lib/core';
import type { Construct } from 'constructs';

export class OpsStack extends Stack {
  constructor(scope: Construct, id: string, props: StackProps) {
    super(scope, id, {
      ...props,
      description:
        'Stockbot ops (logs, alarms, SNS, budgets). Skeleton stack with no resources yet.',
    });
  }
}
