import { Stack, type StackProps } from 'aws-cdk-lib/core';
import type { Construct } from 'constructs';

export class IdentityStack extends Stack {
  constructor(scope: Construct, id: string, props: StackProps) {
    super(scope, id, {
      ...props,
      description: 'Stockbot identity (Cognito, SES). Skeleton stack with no resources yet.',
    });
  }
}
