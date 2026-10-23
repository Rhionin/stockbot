import { Stack, type StackProps } from 'aws-cdk-lib/core';
import type { Construct } from 'constructs';

export class DataStack extends Stack {
  constructor(scope: Construct, id: string, props: StackProps) {
    super(scope, id, {
      ...props,
      description:
        'Stockbot data (Aurora DSQL, backups, exports bucket, KMS). Skeleton stack with no resources yet.',
    });
  }
}
