import { ApiError } from '../../api/client';
import { BUSY_SENTENCE, NO_SPACE_SENTENCE, UnreachableError } from '@stockbot/shared/contract';
import { groupGoalCopy } from './groupGoalCopy';
import { validateGoalField } from './groupGoalValidation';
import type { EditorKind } from './groupGoalModel';

export type SaveErrorKind =
  | 'field'
  | 'save'
  | 'reload_blockers'
  | 'conflict'
  | 'busy'
  | 'no_space';

export interface MappedSaveError {
  kind: SaveErrorKind;
  message: string;
  fieldMessage?: string;
  blockers?: ApiError['blockers'];
}

export function mapSaveError(
  error: unknown,
  editorKind: EditorKind,
  value: number | string,
  amountUnit: 'oz' | 'fl oz' | null,
): MappedSaveError {
  if (error instanceof UnreachableError) {
    return { kind: 'save', message: groupGoalCopy.h2 };
  }
  if (!(error instanceof ApiError)) {
    return { kind: 'save', message: groupGoalCopy.h1 };
  }
  if (error.code === 'busy') {
    return { kind: 'busy', message: BUSY_SENTENCE };
  }
  if (error.code === 'no_space') {
    return { kind: 'no_space', message: NO_SPACE_SENTENCE };
  }
  if (error.code === 'conflict') {
    return { kind: 'conflict', message: groupGoalCopy.h3 };
  }
  if (error.code === 'invalid' && error.field === 'value') {
    const checked = validateGoalField(editorKind, value, amountUnit);
    if (!checked.valid) {
      return { kind: 'field', message: checked.message, fieldMessage: checked.message };
    }
    return { kind: 'field', message: groupGoalCopy.h1 };
  }
  if (error.code === 'amount_unavailable' || error.code === 'units_mismatch') {
    return {
      kind: 'reload_blockers',
      message: '',
      blockers: error.blockers,
    };
  }
  if (error.code === 'invalid') {
    return { kind: 'save', message: groupGoalCopy.h1 };
  }
  return { kind: 'save', message: groupGoalCopy.h1 };
}