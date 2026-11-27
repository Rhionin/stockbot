import { useCallback, useRef, useState } from 'react';
import type { GroupTarget, ProductGroup } from '@stockbot/shared/types';
import { GroupGoalEditor } from './GroupGoalEditor';
import { GroupGoalReadPills } from './GroupGoalReadPills';
import { GroupGoalStatusCard } from './GroupGoalStatusCard';
import type { EditorKind } from './groupGoalModel';
import { resolveGroupGoal } from './groupGoalModel';
import { groupGoalCopy } from './groupGoalCopy';

export function KeepOnHandBlock({
  group,
  editing,
  onEditingChange,
  onSaveRequest,
  onReload,
  onGroupUpdated,
  onFixSize,
  editorSeed,
}: {
  group: ProductGroup;
  editing: boolean;
  onEditingChange: (open: boolean) => void;
  onSaveRequest: (body: GroupTarget) => Promise<ProductGroup>;
  onReload: () => Promise<ProductGroup>;
  onGroupUpdated: (next: ProductGroup) => void;
  onFixSize?: (productId: string) => void;
  editorSeed?: { kind?: EditorKind; emptyAmount?: boolean };
}) {
  const resolved = resolveGroupGoal(group);
  const goalRef = useRef<HTMLButtonElement>(null);
  const [forcedKind, setForcedKind] = useState<EditorKind | undefined>(undefined);
  const [forceEmptyAmount, setForceEmptyAmount] = useState(false);

  const closeEditor = useCallback(() => {
    onEditingChange(false);
    setForcedKind(undefined);
    setForceEmptyAmount(false);
    goalRef.current?.focus();
  }, [onEditingChange]);

  const openEditor = useCallback((kind?: EditorKind, emptyAmount?: boolean) => {
    setForcedKind(kind ?? editorSeed?.kind);
    setForceEmptyAmount(emptyAmount ?? editorSeed?.emptyAmount ?? false);
    onEditingChange(true);
  }, [editorSeed?.emptyAmount, editorSeed?.kind, onEditingChange]);

  const openCountItems = () => openEditor('items', true);

  const openSwitchAmount = () => openEditor('amount', true);

  return (
    <div className="keep-on-hand-block" id="keep">
      {!editing && <p className="seesaw-caption">{groupGoalCopy.e0}</p>}
      {editing ? (
        <GroupGoalEditor
          group={group}
          resolved={resolved}
          forcedKind={forcedKind}
          forceEmptyAmount={forceEmptyAmount}
          onCancel={closeEditor}
          onSaved={(next) => {
            onGroupUpdated(next);
            onEditingChange(false);
            setForcedKind(undefined);
            setForceEmptyAmount(false);
            goalRef.current?.focus();
          }}
          onSaveRequest={onSaveRequest}
          onReload={onReload}
          onFixSize={onFixSize}
        />
      ) : (
        <>
          <GroupGoalReadPills
            group={group}
            resolved={resolved}
            goalRef={goalRef}
            onOpenEditor={() => openEditor()}
          />
          <GroupGoalStatusCard
            group={group}
            resolved={resolved}
            onCountItemsInstead={openCountItems}
            onSwitchAmountUnit={
              resolved.amountAvailable && resolved.amountUnit ? openSwitchAmount : undefined
            }
            onFixSize={onFixSize}
          />
        </>
      )}
    </div>
  );
}