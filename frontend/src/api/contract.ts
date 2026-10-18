// Commit outcomes shared by the web queue and the Expo app.
// requestedCount and removedCount are provisional until the backend confirms them.
import type { RemovalCounts } from '../types';

export const NO_CLOSE_MATCH = 'No close match. Search or add it as new.';

export const BATCH_FAILURE_FALLBACK = 'Some scans could not be confirmed.';

const KNOWN_CODE_MESSAGE: Record<string, string> = {
  instance_gone: 'That one was already taken out. Pick another.',
  none_on_hand: 'Nothing on hand to take out.',
};

export interface CommitConflictBehavior {
  linkOffer: boolean;
  refreshInstances: boolean;
  showMessage: boolean;
}

// Branch on the server's code. An absent or unknown code keeps today's 409:
// show the message, and leave instance refresh and the link offer alone.
export function commitConflictBehavior(code: string | undefined): CommitConflictBehavior {
  switch (code) {
    case 'instance_gone':
      return { linkOffer: false, refreshInstances: true, showMessage: true };
    case 'none_on_hand':
      return { linkOffer: false, refreshInstances: false, showMessage: true };
    case 'no_item':
      return { linkOffer: true, refreshInstances: false, showMessage: false };
    default:
      return { linkOffer: false, refreshInstances: false, showMessage: true };
  }
}

export interface BatchFailureNotice {
  id: string;
  message: string;
  refreshInstances: boolean;
}

// Pair each failed id with its reason. A blank reason uses the shared fallback.
// A reason may be the sentence the server sends today, or a code (string or
// object) if the batch payload grows to match a single-card 409.
export function batchFailureNotices(response: {
  failedIds?: string[];
  failedReasons?: unknown;
}): BatchFailureNotice[] {
  const ids = response.failedIds ?? [];
  const reasons = Array.isArray(response.failedReasons) ? response.failedReasons : [];
  return ids.map((id, index) => {
    const parsed = readFailureReason(reasons[index]);
    return {
      id,
      message: parsed.message !== '' ? parsed.message : BATCH_FAILURE_FALLBACK,
      refreshInstances: parsed.refreshInstances,
    };
  });
}

function readFailureReason(reason: unknown): { message: string; refreshInstances: boolean } {
  if (typeof reason === 'string') {
    const text = reason.trim();
    if (Object.prototype.hasOwnProperty.call(KNOWN_CODE_MESSAGE, text)) {
      return {
        message: KNOWN_CODE_MESSAGE[text],
        refreshInstances: commitConflictBehavior(text).refreshInstances,
      };
    }
    return { message: text, refreshInstances: false };
  }
  if (reason !== null && typeof reason === 'object') {
    const record = reason as Record<string, unknown>;
    const code = typeof record.code === 'string' ? record.code : '';
    const sentence = [record.reason, record.message, record.error].find(
      (value): value is string => typeof value === 'string' && value.trim() !== '',
    );
    const behavior = commitConflictBehavior(code);
    const known = Object.prototype.hasOwnProperty.call(KNOWN_CODE_MESSAGE, code)
      ? KNOWN_CODE_MESSAGE[code]
      : '';
    return {
      message: sentence?.trim() || known,
      refreshInstances: behavior.refreshInstances,
    };
  }
  return { message: '', refreshInstances: false };
}

// n is removedCount. Equal or missing counts say nothing, which is today's confirm.
export function formatPartialRemoval(counts: Partial<RemovalCounts> | null | undefined): string | null {
  if (counts === null || counts === undefined) return null;
  const { requestedCount, removedCount } = counts;
  if (typeof requestedCount !== 'number' || typeof removedCount !== 'number') return null;
  if (!Number.isInteger(requestedCount) || !Number.isInteger(removedCount)) return null;
  if (removedCount < 0 || removedCount >= requestedCount) return null;
  if (removedCount === 1) return 'Only 1 was on hand, so 1 was taken out.';
  return `Only ${removedCount} were on hand, so ${removedCount} were taken out.`;
}

// A successful stock-out, single or batch, may carry the provisional counts on
// the response itself or on a per-scan result entry.
export function partialRemovalNotices(response: object | null | undefined): string[] {
  if (response === null || response === undefined) return [];
  const notes: string[] = [];
  const top = formatPartialRemoval(response as RemovalCounts);
  if (top !== null) notes.push(top);
  for (const value of Object.values(response)) {
    if (!Array.isArray(value)) continue;
    for (const item of value) {
      if (item === null || typeof item !== 'object') continue;
      const note = formatPartialRemoval(item as RemovalCounts);
      if (note !== null) notes.push(note);
    }
  }
  return [...new Set(notes)];
}
