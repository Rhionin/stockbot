import { afterEach } from '@jest/globals';
import { clearServerNotice } from '../../../frontend/src/api/contract';

afterEach(() => {
  clearServerNotice();
});
