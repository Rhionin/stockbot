import { afterEach, describe, expect, it, vi } from 'vitest';
import { applyToastClearance } from './undo';

describe('toast clearance', () => {
  afterEach(() => {
    vi.unstubAllGlobals();
  });

  it('does nothing when the document is already gone', () => {
    vi.stubGlobal('document', undefined);
    expect(typeof document).toBe('undefined');
    expect(() => applyToastClearance()).not.toThrow();
  });
});
