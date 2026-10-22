export interface BuildStampInfo {
  version: string;
  updateId: string;
  runtimeVersion: string;
}

export function describeBuild(input: {
  nativeApplicationVersion: string | null;
  nativeBuildVersion: string | null;
  configVersion: string | undefined;
  updateId: string | null;
  runtimeVersion: string | null;
  updatesEnabled: boolean;
}): BuildStampInfo {
  const installedVersion = input.updatesEnabled ? input.nativeApplicationVersion : null;
  const versionName = blankToNull(installedVersion) ?? blankToNull(input.configVersion) ?? 'unknown';
  const versionCode = input.updatesEnabled ? blankToNull(input.nativeBuildVersion) : null;
  return {
    version: versionCode === null ? versionName : `${versionName} (${versionCode})`,
    updateId: blankToNull(input.updateId) ?? 'embedded',
    runtimeVersion: blankToNull(input.runtimeVersion) ?? 'unavailable',
  };
}

function blankToNull(value: string | null | undefined): string | null {
  if (value == null) return null;
  const trimmed = value.trim();
  return trimmed === '' ? null : trimmed;
}
