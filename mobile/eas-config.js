// Plain JavaScript because Expo loads app.config.ts with Node's require,
// which does not resolve a TypeScript import.

function easUpdateUrl(projectId) {
  if (typeof projectId !== 'string') return null;
  const trimmed = projectId.trim();
  if (trimmed === '') return null;
  return `https://u.expo.dev/${trimmed}`;
}

function projectIdFrom(extra) {
  if (extra == null || typeof extra !== 'object') return null;
  const projectId = extra.eas?.projectId;
  return projectId ?? null;
}

function withEasUpdates(config) {
  const url = easUpdateUrl(projectIdFrom(config.extra));
  const updates = { ...(config.updates ?? {}) };
  delete updates.url;
  return {
    ...config,
    runtimeVersion: { policy: 'fingerprint' },
    updates: {
      ...updates,
      enabled: url !== null,
      checkAutomatically: 'ON_LOAD',
      fallbackToCacheTimeout: 0,
      ...(url === null ? {} : { url }),
    },
  };
}

module.exports = { easUpdateUrl, withEasUpdates };
