import { Stack, type StackProps } from 'aws-cdk-lib/core';
import type { Construct } from 'constructs';

export class EdgeStack extends Stack {
  constructor(scope: Construct, id: string, props: StackProps) {
    super(scope, id, {
      ...props,
      description: 'Stockbot edge (CloudFront, WAF, DNS). Skeleton stack with no resources yet.',
    });
  }
}
