// TODO(#389): use the shared offline helper when the web client has it.
// A network failure stays this sentence, with no raw browser message.
export const OFFLINE_SENTENCE = "Can't reach Stockbot. Check your connection and try again.";

export function offlineMessage(error: unknown, fallback: string): string {
  if (error instanceof TypeError) return OFFLINE_SENTENCE;
  if (error instanceof Error && error.message !== '') return error.message;
  return fallback;
}
