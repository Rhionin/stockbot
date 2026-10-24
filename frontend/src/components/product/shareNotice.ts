import { notifications } from '@mantine/notifications';
import type { ProductWriteResult } from '@stockbot/shared/types';

export const showShareNotice = (result: ProductWriteResult): void => {
  const detail = result.contribution?.detail;
  if (!detail) return;
  notifications.show({
    message: detail,
    color: result.contribution?.status === 'submitted' ? 'green' : 'yellow',
  });
};
