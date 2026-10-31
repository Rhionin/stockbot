/** Coalesce concurrent saves for the same row id (e.g. parallel flushes both committing a pending choice). */
export function dedupeConcurrentSave<T>(
  inFlight: Record<string, Promise<T>>,
  entryId: string,
  start: () => Promise<T>,
): Promise<T> {
  const existing = inFlight[entryId];
  if (existing !== undefined) return existing;
  const saving = start();
  inFlight[entryId] = saving;
  return saving;
}
