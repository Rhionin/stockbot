import type { Ref, ReactNode } from 'react';
import type { ProductGroup } from '@stockbot/shared/types';
import { formatDuration } from './seesaw';
import { goalPillLabel, groupGoalCopy } from './groupGoalCopy';
import type { ResolvedGroupGoal } from './groupGoalModel';

function PinIcon() {
  return (
    <svg className="seesaw-pin" viewBox="0 0 24 24" aria-hidden="true">
      <path
        fill="currentColor"
        d="M16 12V4h1V2H7v2h1v8l-2 2v2h5.2v6h1.6v-6H18v-2l-2-2z"
      />
    </svg>
  );
}

export function GroupGoalReadPills({
  group,
  resolved,
  onOpenEditor,
  goalRef,
}: {
  group: ProductGroup;
  resolved: ResolvedGroupGoal;
  onOpenEditor: () => void;
  goalRef?: Ref<HTMLButtonElement>;
}) {
  const target = resolved.target;
  const usage = group.usage;

  if (target?.kind === 'dont_restock') {
    return (
      <div className="group-goal-read">
        <p className="group-goal-dont-copy">{groupGoalCopy.groupDontRestock(group.name)}</p>
        <ButtonGoal type="button" onClick={onOpenEditor} label={groupGoalCopy.e12} ref={goalRef}>
          {groupGoalCopy.e12}
        </ButtonGoal>
      </div>
    );
  }

  if (resolved.target === null && resolved.defaultKind === 'dont_restock') {
    return (
      <div className="group-goal-read">
        <ButtonGoal
          type="button"
          onClick={onOpenEditor}
          label={groupGoalCopy.e10}
          pinned
          ref={goalRef}
        >
          {groupGoalCopy.e10}
        </ButtonGoal>
        <p className="group-goal-hint">{groupGoalCopy.e11}</p>
      </div>
    );
  }

  const primaryLabel = target
    ? goalPillLabel(target.kind, target.value, target.unit)
    : resolved.defaultKind === 'amount'
      ? '3 mo'
      : `${resolved.defaultMonths} mo`;

  const primaryPinned = target !== null;
  const showDefaultSuffix = !primaryPinned && resolved.defaultKind !== 'dont_restock';
  const accessibleLabel = showDefaultSuffix ? `${primaryLabel} default` : primaryLabel;
  const primaryDescription = target
    ? `Keep on hand count, pinned, ${primaryLabel}`
    : `Keep on hand time, pinned, ${resolved.defaultMonths} months, household default`;

  let secondary: string = groupGoalCopy.fillOnceKnown;
  if (usage && usage.perMonth > 0 && target) {
    const goalUnit = target.unit ?? '';
    if (
      (target.kind === 'items' && usage.unit === 'items')
      || (target.kind === 'months' && (usage.unit === 'oz' || usage.unit === 'fl oz'))
      || (target.kind === 'amount' && usage.unit === goalUnit)
    ) {
      if (target.kind === 'months' || (target.kind === 'amount' && usage.unit === goalUnit)) {
        const months = target.kind === 'months' ? target.value : target.value / usage.perMonth;
        const est = formatDuration(months);
        if (est !== '') secondary = `≈ ${est}`;
      }
      if (target.kind === 'items') {
        secondary = `at ${usage.perMonth} items / mo`;
      }
    }
  }

  return (
    <div className="group-goal-read seesaw-row">
      <div className="seesaw-end">
        <ButtonGoal
          type="button"
          onClick={onOpenEditor}
          label={accessibleLabel}
          description={primaryDescription}
          pinned={primaryPinned}
          ref={goalRef}
        >
          {primaryPinned && <PinIcon />}
          <span>{primaryLabel}</span>
          {showDefaultSuffix && (
            <span className="seesaw-default">{' '}default</span>
          )}
        </ButtonGoal>
      </div>
      <div className="seesaw-beam">
        <span className="seesaw-beam-line" />
      </div>
      <div className="seesaw-end">
        <span className="seesaw-pill is-inferred" aria-hidden="true">
          {secondary}
        </span>
      </div>
      {resolved.target === null && resolved.defaultKind === 'amount' && (
        <p className="group-goal-hint">{groupGoalCopy.d2f}</p>
      )}
    </div>
  );
}

function ButtonGoal({
  children,
  label,
  description,
  pinned,
  onClick,
  ref,
  type,
}: {
  children: ReactNode;
  label: string;
  description?: string;
  pinned?: boolean;
  onClick: () => void;
  ref?: Ref<HTMLButtonElement>;
  type: 'button';
}) {
  const descriptionId = description ? `${label}-desc` : undefined;
  return (
    <>
      <button
        ref={ref}
        type={type}
        className={pinned ? 'seesaw-pill is-pinned' : 'seesaw-pill is-inferred'}
        aria-pressed={pinned}
        aria-label={label}
        aria-describedby={descriptionId}
        onClick={onClick}
      >
        {children}
      </button>
      {description && descriptionId && (
        <span id={descriptionId} className="seesaw-description">{description}</span>
      )}
    </>
  );
}