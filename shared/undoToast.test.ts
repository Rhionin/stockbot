import { describe, expect, it } from 'vitest';
import { UNDO_TOAST_MS, undoStillOffered, undoToastAutoClose, undoToastId } from './undoToast';

describe('undo toast', () => {
  it('closes after five seconds unless told otherwise', () => {
    expect(UNDO_TOAST_MS).toBe(5000);
    expect(undoToastAutoClose({ detail: 'x' })).toBe(5000);
    expect(undoToastAutoClose({ detail: 'x', autoClose: false })).toBe(false);
    expect(undoToastAutoClose({ detail: 'x', autoClose: 800 })).toBe(800);
  });
  it('keeps a caller id and otherwise builds one', () => {
    expect(undoToastId({ detail: 'x', id: 'mine' }, 3, 10)).toBe('mine');
    expect(undoToastId({ detail: 'x' }, 3, 10)).toBe('restock-10-3');
  });
  it('refuses an Undo whose toast is gone or replaced', () => {
    expect(undoStillOffered('a', 'a')).toBe(true);
    expect(undoStillOffered(null, 'a')).toBe(false);
    expect(undoStillOffered('b', 'a')).toBe(false);
  });
});
