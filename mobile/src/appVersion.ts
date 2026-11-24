import Constants from 'expo-constants';

export const APP_VERSION: string = Constants.expoConfig?.version ?? '0.0.0';

function parts(version: string): number[] {
  return version.split('-')[0]!.split('.').map((part) => Number.parseInt(part, 10) || 0);
}

export function isOlderVersion(version: string, minimum: string): boolean {
  const have = parts(version);
  const need = parts(minimum);
  for (let i = 0; i < Math.max(have.length, need.length); i += 1) {
    const diff = (have[i] ?? 0) - (need[i] ?? 0);
    if (diff !== 0) return diff < 0;
  }
  return false;
}
