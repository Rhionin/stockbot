import type { IConstruct } from 'constructs';
import { Validations, type IPolicyValidationPlugin } from 'aws-cdk-lib/core';
import { AwsSolutionsChecks } from 'cdk-nag';

/**
 * cdk-nag v3 is a validation plugin. Register it on the stage, not the app:
 * the app assembly has no stacks of its own, so a plugin on the app never runs.
 * validateScope is limited to this stage so dev and prod are not reported twice.
 */
export function addAwsSolutionsChecks(stage: IConstruct): void {
  const pack = new AwsSolutionsChecks(stage, { verbose: true });
  const plugin: IPolicyValidationPlugin = {
    name: pack.name,
    version: pack.version,
    ruleIds: pack.ruleIds,
    validate: () => pack.validateScope(stage),
  };
  Validations.of(stage).addPlugins(plugin);
}
