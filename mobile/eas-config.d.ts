import type { ExpoConfig } from 'expo/config';

export function easUpdateUrl(projectId: unknown): string | null;

export function withEasUpdates(config: Partial<ExpoConfig>): ExpoConfig;
