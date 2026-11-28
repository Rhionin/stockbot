import type { AuthSession } from '@stockbot/shared/contract';
import type { ShoppingLineEdited } from '@stockbot/shared/types';
import { g2Named, g2You, g3 } from '../groups/groupGoalCopy';

export function editedTripDescription(
  edited: ShoppingLineEdited,
  session: AuthSession | null | undefined,
): string {
  const userId = session?.user?.id;
  if (edited.actorUserId && userId && edited.actorUserId === userId) return g2You;
  if (edited.actorDisplayName) return g2Named(edited.actorDisplayName);
  return g3;
}