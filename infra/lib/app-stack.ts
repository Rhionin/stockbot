import { Stack, type StackProps } from 'aws-cdk-lib/core';
import type { Construct } from 'constructs';

export class AppStack extends Stack {
  constructor(scope: Construct, id: string, props: StackProps) {
    super(scope, id, {
      ...props,
      description:
        'Stockbot app (Lambda API, jobs, migrate, AppSync Events, Scheduler). Skeleton stack with no resources yet.',
    });
  }
}
