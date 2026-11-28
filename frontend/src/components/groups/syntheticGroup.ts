import type { GroupGoalTarget, ProductGroup } from '@stockbot/shared/types';

export function goalTarget(
  partial: Omit<GroupGoalTarget, 'blockers'> & { blockers?: GroupGoalTarget['blockers'] },
): GroupGoalTarget {
  return { ...partial, blockers: partial.blockers ?? [] };
}

export function syntheticGroup(overrides: Partial<ProductGroup> = {}): ProductGroup {
  return {
    id: 'g1',
    name: 'Test group',
    rule: 'same_as_ran_out',
    ruleConfirmed: false,
    members: [],
    runningLow: false,
    target: null,
    amountAvailable: false,
    amountUnit: null,
    amountBlockers: [],
    defaultKind: 'months',
    defaultMonths: 3,
    version: '',
    ...overrides,
  };
}