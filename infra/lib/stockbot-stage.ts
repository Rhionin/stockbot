import { Stack, Stage, Tags, Validations, type StageProps, type TagProps } from 'aws-cdk-lib/core';
import type { Construct } from 'constructs';
import { AppStack } from './app-stack.js';
import { DataStack } from './data-stack.js';
import { EdgeStack } from './edge-stack.js';
import { IdentityStack } from './identity-stack.js';
import { OpsStack } from './ops-stack.js';
import { addAwsSolutionsChecks } from './stage-nag.js';
import type { StageName } from './stage-env.js';

export interface StockbotStageProps extends StageProps {
  readonly stageName: StageName;
}

export class StockbotStage extends Stage {
  constructor(scope: Construct, id: string, props: StockbotStageProps) {
    super(scope, id, props);

    const stackProps = { env: props.env };

    // explicitStackTags drops stack tags unless the tag includes aws:cdk:stack.
    // CloudFormation also rejects an empty Resources section; these stacks stay
    // empty until a later change adds resources.
    const stackTag: TagProps = { includeResourceTypes: ['aws:cdk:stack'] };
    const finishSkeleton = (stack: Stack): void => {
      Tags.of(stack).add('app', 'stockbot', stackTag);
      Tags.of(stack).add('stage', props.stageName, stackTag);
      Tags.of(stack).add('owner', 'network-engineer', stackTag);
      Validations.of(stack).acknowledge({
        id: 'CloudFormation-Validate::F0001',
        reason:
          'Skeleton stack with no resources yet. The finding clears once this stack has resources.',
      });
    };

    finishSkeleton(new EdgeStack(this, 'Edge', stackProps));
    finishSkeleton(new DataStack(this, 'Data', stackProps));
    finishSkeleton(new IdentityStack(this, 'Identity', stackProps));
    finishSkeleton(new AppStack(this, 'App', stackProps));
    finishSkeleton(new OpsStack(this, 'Ops', stackProps));

    addAwsSolutionsChecks(this);
  }
}
