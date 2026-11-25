import { addProcessingNotice, removeProcessingNotice, settleProcessingNotice } from './queueUtils';
import type { ProcessingFailure, ProcessingNotice, ScanEntry } from './types';

// The web keeps only the live notices. The phone also remembers the ids that
// already settled or failed, so a duplicate or late event cannot bring a
// finished lookup back.
export interface ProcessingState {
  notices: ProcessingNotice[];
  finished: string[];
}

export const emptyProcessing: ProcessingState = { notices: [], finished: [] };

const FINISHED_KEPT = 100;

const finish = (finished: string[], ids: string[]): string[] =>
  [...finished, ...ids].slice(-FINISHED_KEPT);

export const processingAdd = (state: ProcessingState, notice: ProcessingNotice): ProcessingState => {
  if (state.finished.includes(notice.id)) return state;
  const notices = addProcessingNotice(state.notices, notice);
  return notices === state.notices ? state : { ...state, notices };
};

export const processingSettle = (
  state: ProcessingState,
  event: Pick<ScanEntry, 'barcode' | 'direction' | 'status'>,
): ProcessingState => {
  const notices = settleProcessingNotice(state.notices, event);
  if (notices === state.notices) return state;
  const gone = state.notices.filter((notice) => !notices.includes(notice)).map((notice) => notice.id);
  return { notices, finished: finish(state.finished, gone) };
};

// A failure for an id the queue never showed is ignored, message included.
export const processingFail = (
  state: ProcessingState,
  failure: ProcessingFailure,
): { state: ProcessingState; message: string | null } => {
  if (!state.notices.some((notice) => notice.id === failure.id)) return { state, message: null };
  return {
    state: {
      notices: removeProcessingNotice(state.notices, failure.id),
      finished: finish(state.finished, [failure.id]),
    },
    message: failure.message,
  };
};
