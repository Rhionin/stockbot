import { notifications } from '@mantine/notifications';
import { shareNoticeFor } from '@stockbot/shared/productForm';
import type { ProductWriteResult } from '@stockbot/shared/types';

export const showShareNotice = (result: ProductWriteResult): void => {
  const notice = shareNoticeFor(result);
  if (notice === null) return;
  notifications.show({
    message: notice.message,
    color: notice.tone === 'submitted' ? 'green' : 'yellow',
  });
};
