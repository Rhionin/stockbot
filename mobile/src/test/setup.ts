import { afterEach } from '@jest/globals';
import { clearOfflineBanner } from '../../../frontend/src/api/contract';

afterEach(() => {
  clearOfflineBanner();
});
