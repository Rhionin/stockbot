import { describe, expect, it } from 'vitest';
import { collectKrogerAppCopyStrings, KROGER_APP_COPY_BANNED } from '@stockbot/shared/krogerAppCopy';

describe('T50 kroger app copy jargon', () => {
  it('T50 keeps banned jargon out of c.app source strings', () => {
    for (const line of collectKrogerAppCopyStrings()) {
      expect(line, line).not.toMatch(KROGER_APP_COPY_BANNED);
    }
  });
});
