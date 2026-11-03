import { describe, expect, it } from 'vitest';
import { krogerAppCopy, krogerAppModalityHelp, storeNameContext } from './krogerAppCopy';

describe('c.app.modality copy', () => {
  it('uses the store name in the helper when linked', () => {
    const ctx = storeNameContext('Kroger');
    expect(krogerAppModalityHelp(ctx)).toContain('Kroger');
    expect(krogerAppModalityHelp(ctx)).toContain('Change it anytime here.');
  });

  it('uses the fallback helper without a store name', () => {
    const ctx = storeNameContext(null);
    expect(krogerAppModalityHelp(ctx)).toBe(krogerAppCopy.modalityHelpFallback);
  });
});
