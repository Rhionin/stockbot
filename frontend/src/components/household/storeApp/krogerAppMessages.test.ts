import { describe, expect, it } from 'vitest';
import { hostedErrorSentence } from '@stockbot/shared/contract';
import { krogerAppCopy } from '@stockbot/shared/krogerAppCopy';
import { ApiError } from '../../../api/client';
import {
  mapKrogerAppApiError,
  rowLoadFailureMessage,
  storeDisabledUserMessage,
} from './krogerAppMessages';

describe('mapKrogerAppApiError', () => {
  it('maps store_disabled by code', () => {
    expect(mapKrogerAppApiError(new ApiError(409, 'ignored', 'store_disabled'))).toEqual({ kind: 'store_disabled' });
  });

  it('maps forbidden_role by code', () => {
    expect(mapKrogerAppApiError(new ApiError(403, 'Only an owner can do that.', 'forbidden_role'))).toEqual({
      kind: 'forbidden_role',
    });
  });
});

describe('storeDisabledUserMessage', () => {
  it('uses hosted store_disabled copy', () => {
    expect(storeDisabledUserMessage()).toBe(hostedErrorSentence('store_disabled'));
  });
});

describe('rowLoadFailureMessage', () => {
  it('returns row load copy', () => {
    expect(rowLoadFailureMessage(new ApiError(404, 'missing', 'not_found'))).toBe(krogerAppCopy.rowLoadFailed);
    expect(rowLoadFailureMessage(null)).toBe(krogerAppCopy.rowLoadFailed);
  });
});
