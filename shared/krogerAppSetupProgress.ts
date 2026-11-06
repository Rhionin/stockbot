export type KrogerAppSetupReturnTo = `link:${string}` | 'household' | '';

export interface KrogerAppSetupProgress {
  step: number;
  done: number[];
  suggestedName: string;
  returnTo: KrogerAppSetupReturnTo;
  updatedAt: string;
}

const storageKey = (householdId: string) => `stockbot.krogerAppSetup.${householdId}`;

export function readKrogerAppSetupProgress(householdId: string): KrogerAppSetupProgress | null {
  if (householdId === '' || typeof localStorage === 'undefined') return null;
  try {
    const raw = localStorage.getItem(storageKey(householdId));
    if (raw === null) return null;
    const parsed = JSON.parse(raw) as KrogerAppSetupProgress;
    if (typeof parsed.step !== 'number') return null;
    return parsed;
  } catch {
    return null;
  }
}

export function writeKrogerAppSetupProgress(householdId: string, progress: KrogerAppSetupProgress): void {
  if (householdId === '') return;
  localStorage.setItem(storageKey(householdId), JSON.stringify(progress));
}

export function clearKrogerAppSetupProgress(householdId: string): void {
  if (householdId === '') return;
  localStorage.removeItem(storageKey(householdId));
}

/** Keys and secrets must never appear in persisted progress (T43). */
export function assertProgressHasNoSecrets(progress: KrogerAppSetupProgress): void {
  const blob = JSON.stringify(progress).toLowerCase();
  for (const forbidden of ['clientid', 'clientsecret', 'secret', 'password']) {
    if (blob.includes(forbidden)) {
      throw new Error(`progress must not contain ${forbidden}`);
    }
  }
}
