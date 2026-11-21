import { describe, it } from 'vitest';
import type { NeedsAttentionReason } from './types';

// @ts-expect-error legacy wire reason is not in the closed union
const _needsAttentionReasonLock: NeedsAttentionReason = 'attention';
void _needsAttentionReasonLock;

describe('NeedsAttentionReason compile-time lock', () => {
  it('is enforced by npm run typecheck', () => {});
});