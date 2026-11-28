import { type KeyboardEvent, type ReactNode, type Ref, useEffect, useId, useRef, useState } from 'react';
import { Button, Group, NumberInput, Stack, Text } from '@mantine/core';
import type { GroupGoalBlocker, GroupTarget, ProductGroup } from '@stockbot/shared/types';
import { ApiError } from '../../api/client';
import {
  amountChoiceLabel,
  amountFieldLabel,
  amountUnavailableHeadline,
  b1,
  b2,
  b3,
  b4A11y,
  b4Label,
  b5A11y,
  b5Label,
  groupGoalCopy,
} from './groupGoalCopy';
import type { EditorKind, ResolvedGroupGoal } from './groupGoalModel';
import { buildPutBody, initialEditorKind, prefillValue } from './groupGoalModel';
import { mapSaveError } from './groupGoalSaveErrors';
import { validateGoalField } from './groupGoalValidation';

type DraftValues = Record<EditorKind, number | string>;

function emptyDrafts(resolved: ResolvedGroupGoal, forceEmptyAmount: boolean): DraftValues {
  return {
    items: prefillValue('items', resolved),
    months: prefillValue('months', resolved),
    amount: forceEmptyAmount ? '' : prefillValue('amount', resolved),
  };
}

function blockerLine(row: GroupGoalBlocker): string {
  return row.reason === 'size_missing' ? b1(row.name) : row.sizeKind === 'volume' ? b2(row.name) : b3(row.name);
}

export function GroupGoalEditor({
  group,
  resolved,
  forcedKind,
  forceEmptyAmount = false,
  onCancel,
  onSaved,
  onSaveRequest,
  onReload,
  onFixSize,
}: {
  group: ProductGroup;
  resolved: ResolvedGroupGoal;
  forcedKind?: EditorKind;
  forceEmptyAmount?: boolean;
  onCancel: () => void;
  onSaved: (next: ProductGroup) => void;
  onSaveRequest: (body: GroupTarget) => Promise<ProductGroup>;
  onReload: () => Promise<ProductGroup>;
  onFixSize?: (productId: string) => void;
}) {
  const baseKind = forcedKind ?? initialEditorKind(resolved);
  const [kind, setKind] = useState<EditorKind>(baseKind);
  const [drafts, setDrafts] = useState<DraftValues>(() => emptyDrafts(resolved, forceEmptyAmount));
  const [fieldError, setFieldError] = useState('');
  const [saveError, setSaveError] = useState('');
  const [showConflictRefresh, setShowConflictRefresh] = useState(false);
  const [saving, setSaving] = useState(false);
  const [amountBlockers, setAmountBlockers] = useState<GroupGoalBlocker[]>(group.amountBlockers ?? []);
  const [amountForcedUnavailable, setAmountForcedUnavailable] = useState(false);
  const fieldRef = useRef<HTMLInputElement>(null);
  const legendId = useId();

  useEffect(() => {
    fieldRef.current?.focus();
  }, [kind]);

  useEffect(() => {
    if (forcedKind === undefined) return;
    // Open from status card actions (items / amount switch).
    const timer = window.setTimeout(() => {
      setKind(forcedKind);
      if (forceEmptyAmount) setDrafts((prev) => ({ ...prev, amount: '' }));
    }, 0);
    return () => window.clearTimeout(timer);
  }, [forcedKind, forceEmptyAmount]);

  const { amountUnit } = resolved;
  const amountAvailable = resolved.amountAvailable && !amountForcedUnavailable;

  const pickKind = (next: EditorKind) => {
    if (next === 'amount' && !amountAvailable) return;
    setKind(next);
    setFieldError('');
    setSaveError('');
  };

  const save = async () => {
    setFieldError('');
    setSaveError('');
    setShowConflictRefresh(false);
    const raw = drafts[kind];
    const checked = validateGoalField(kind, raw, amountUnit);
    if (!checked.valid) {
      setFieldError(checked.message);
      fieldRef.current?.focus();
      return;
    }
    const value = typeof raw === 'number' ? raw : Number(raw);
    setSaving(true);
    try {
      const next = await onSaveRequest(buildPutBody(kind, value, resolved.version));
      onSaved(next);
    } catch (error) {
      const mapped = mapSaveError(error, kind, raw, amountUnit);
      if (mapped.kind === 'field' && mapped.fieldMessage) {
        setFieldError(mapped.fieldMessage);
      } else if (mapped.kind === 'reload_blockers') {
        try {
          const reloaded = await onReload();
          const blockersFromError = error instanceof ApiError && error.blockers.length > 0
            ? error.blockers
            : reloaded.amountBlockers ?? [];
          setAmountBlockers(blockersFromError);
          setAmountForcedUnavailable(true);
          onSaved(reloaded);
        } catch {
          setSaveError(groupGoalCopy.h1);
        }
      } else if (mapped.kind === 'conflict') {
        setSaveError(mapped.message);
        setShowConflictRefresh(true);
      } else {
        setSaveError(mapped.message);
      }
      fieldRef.current?.focus();
    } finally {
      setSaving(false);
    }
  };

  const refreshAfterConflict = async () => {
    setSaving(true);
    try {
      const reloaded = await onReload();
      onSaved(reloaded);
      setSaveError('');
      setShowConflictRefresh(false);
    } catch {
      setSaveError(groupGoalCopy.h1);
    } finally {
      setSaving(false);
    }
  };

  const blockers = amountBlockers;
  const noMembers = group.members.length === 0;

  return (
    <form
      className="group-goal-editor"
      onSubmit={(event) => {
        event.preventDefault();
        void save();
      }}
    >
      <fieldset className="group-goal-fieldset">
        <legend id={legendId} className="seesaw-caption">{groupGoalCopy.e0}</legend>
        <Stack gap="md" role="radiogroup" aria-labelledby={legendId}>
          <KindChoice
            name="goal-kind"
            value="items"
            checked={kind === 'items'}
            label={groupGoalCopy.e1}
            description={groupGoalCopy.e1d}
            onSelect={() => pickKind('items')}
          >
            {kind === 'items' && (
              <ValueField
                ref={fieldRef}
                label={groupGoalCopy.e4}
                suffix="items"
                inputMode="numeric"
                value={drafts.items}
                error={fieldError}
                onChange={(value) => setDrafts((prev) => ({ ...prev, items: value }))}
                onCancel={onCancel}
                onSave={() => void save()}
              />
            )}
          </KindChoice>
          <KindChoice
            name="goal-kind"
            value="months"
            checked={kind === 'months'}
            label={groupGoalCopy.e2}
            description={groupGoalCopy.e2d}
            onSelect={() => pickKind('months')}
          >
            {kind === 'months' && (
              <ValueField
                ref={fieldRef}
                label="Months"
                suffix="mo"
                inputMode="numeric"
                value={drafts.months}
                error={fieldError}
                onChange={(value) => setDrafts((prev) => ({ ...prev, months: value }))}
                onCancel={onCancel}
                onSave={() => void save()}
              />
            )}
          </KindChoice>
          <KindChoice
            name="goal-kind"
            value="amount"
            checked={kind === 'amount'}
            label={amountChoiceLabel(amountUnit, amountAvailable)}
            description={groupGoalCopy.e3d}
            onSelect={() => pickKind('amount')}
          >
            {!amountAvailable && (
              <Stack gap="xs" className="group-goal-blockers" mt="xs">
                <Text size="sm">{amountUnavailableHeadline(blockers, noMembers)}</Text>
                {blockers.map((row) => (
                  <div key={row.productId} className="group-goal-blocker-row">
                    <Text size="sm">{blockerLine(row)}</Text>
                    {onFixSize && (
                      <Button
                        type="button"
                        size="compact-sm"
                        className="group-goal-fix"
                        aria-label={row.reason === 'size_missing' ? b4A11y(row.name) : b5A11y(row.name)}
                        onClick={() => onFixSize(row.productId)}
                      >
                        {row.reason === 'size_missing' ? b4Label : b5Label}
                      </Button>
                    )}
                  </div>
                ))}
                <Button type="button" variant="subtle" className="group-goal-fix" onClick={() => pickKind('items')}>
                  {groupGoalCopy.b6}
                </Button>
              </Stack>
            )}
            {kind === 'amount' && amountAvailable && (
              <ValueField
                ref={fieldRef}
                label={amountFieldLabel(amountUnit)}
                suffix={amountUnit === 'fl oz' ? 'fl oz' : 'oz'}
                inputMode="decimal"
                value={drafts.amount}
                error={fieldError}
                onChange={(value) => setDrafts((prev) => ({ ...prev, amount: value }))}
                onCancel={onCancel}
                onSave={() => void save()}
              />
            )}
          </KindChoice>
        </Stack>
      </fieldset>
      <Group className="seesaw-actions group-goal-actions" gap="sm" mt="md">
        <Button type="submit" size="sm" loading={saving} aria-busy={saving}>
          {groupGoalCopy.save}
        </Button>
        <Button type="button" size="sm" variant="default" disabled={saving} onClick={onCancel}>
          {groupGoalCopy.cancel}
        </Button>
        {showConflictRefresh && (
          <Button type="button" size="sm" variant="light" onClick={() => void refreshAfterConflict()}>
            {groupGoalCopy.e9}
          </Button>
        )}
      </Group>
      {saveError !== '' && (
        <Text className="seesaw-error group-goal-save-error" size="sm" c="red" role="alert">
          {saveError}
        </Text>
      )}
    </form>
  );
}

function KindChoice({
  name,
  value,
  checked,
  label,
  description,
  onSelect,
  disabled,
  children,
}: {
  name: string;
  value: EditorKind;
  checked: boolean;
  label: string;
  description: string;
  onSelect: () => void;
  disabled?: boolean;
  children?: ReactNode;
}) {
  const descId = useId();
  return (
    <label
      className={`group-goal-choice${checked ? ' is-selected' : ''}${disabled ? ' is-disabled' : ''}`}
      aria-disabled={disabled || undefined}
    >
      <input
        type="radio"
        name={name}
        value={value}
        checked={checked}
        className="group-goal-native-radio"
        onChange={() => onSelect()}
      />
      <span className="group-goal-choice-label">{label}</span>
      <span id={descId} className="group-goal-choice-desc">{description}</span>
      {children}
    </label>
  );
}

function ValueField({
  ref,
  label,
  suffix,
  inputMode,
  value,
  error,
  onChange,
  onCancel,
  onSave,
}: {
  ref?: Ref<HTMLInputElement>;
  label: string;
  suffix: string;
  inputMode: 'numeric' | 'decimal';
  value: number | string;
  error: string;
  onChange: (value: number | string) => void;
  onCancel: () => void;
  onSave: () => void;
}) {
  const errorId = useId();
  const onKeyDown = (event: KeyboardEvent) => {
    if (event.key === 'Escape') {
      event.preventDefault();
      onCancel();
    }
    if (event.key === 'Enter') {
      event.preventDefault();
      onSave();
    }
  };
  return (
    <div className="group-goal-field">
      <NumberInput
        ref={ref}
        className="seesaw-editor"
        aria-label={label}
        aria-invalid={error !== ''}
        aria-describedby={error !== '' ? errorId : undefined}
        value={value}
        onChange={onChange}
        min={0}
        hideControls
        size="sm"
        inputMode={inputMode}
        enterKeyHint="done"
        rightSection={<span className="group-goal-suffix">{suffix}</span>}
        onKeyDown={onKeyDown}
      />
      {error !== '' && (
        <Text id={errorId} size="sm" c="red" mt={4}>
          {error}
        </Text>
      )}
    </div>
  );
}