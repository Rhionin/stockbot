import { afterEach } from '@jest/globals';
import { clearServerNotice } from '@stockbot/shared/contract';

afterEach(() => {
  clearServerNotice();
});
