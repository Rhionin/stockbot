import { describe, expect, it } from 'vitest';
import { krogerAppCopy } from '@stockbot/shared/krogerAppCopy';
import { ApiError } from '../../../api/client';
import {
  mapKrogerAppApiError,
  rowLoadFailureMessage,
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

describe('rowLoadFailureMessage', () => {
  it('returns row load copy', () => {
    expect(rowLoadFailureMessage(new ApiError(404, 'missing', 'not_found'))).toBe(krogerAppCopy.rowLoadFailed);
    expect(rowLoadFailureMessage(null)).toBe(krogerAppCopy.rowLoadFailed);
  });
});
