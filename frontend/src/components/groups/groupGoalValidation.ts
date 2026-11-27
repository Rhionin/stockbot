import type { EditorKind } from './groupGoalModel';
import { groupGoalCopy } from './groupGoalCopy';

export type FieldValidation = { valid: true } | { valid: false; message: string };

export function validateGoalField(
  kind: EditorKind,
  raw: number | string,
  amountUnit: 'oz' | 'fl oz' | null,
): FieldValidation {
  const value = typeof raw === 'number' ? raw : Number(raw);
  if (kind === 'items') {
    if (!Number.isInteger(value) || value < 1 || value > 999) {
      return { valid: false, message: groupGoalCopy.e6 };
    }
    return { valid: true };
  }
  if (kind === 'months') {
    if (!Number.isInteger(value) || value < 1 || value > 12) {
      return { valid: false, message: groupGoalCopy.monthsBad };
    }
    return { valid: true };
  }
  if (!Number.isFinite(value) || value <= 0 || value > 999) {
    return {
      valid: false,
      message: amountUnit === 'fl oz' ? groupGoalCopy.e7 : groupGoalCopy.ozBad,
    };
  }
  const rounded = Math.round(value * 10) / 10;
  if (Math.abs(rounded - value) > 0.0001) {
    return {
      valid: false,
      message: amountUnit === 'fl oz' ? groupGoalCopy.e7 : groupGoalCopy.ozBad,
    };
  }
  return { valid: true };
}