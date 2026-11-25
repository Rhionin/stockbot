import { easUpdateUrl, withEasUpdates } from '../../eas-config';

describe('eas update config', () => {
  it('leaves the update url unset until a project id exists', () => {
    expect(easUpdateUrl(undefined)).toBeNull();
    expect(easUpdateUrl('')).toBeNull();
    expect(easUpdateUrl('   ')).toBeNull();

    const config = withEasUpdates({ name: 'Stockbot', slug: 'stockbot' });
    expect(config.owner).toBeUndefined();
    expect(config.extra?.eas).toBeUndefined();
    expect(config.updates?.url).toBeUndefined();
    expect(config.updates?.enabled).toBe(false);
    expect(config.runtimeVersion).toEqual({ policy: 'fingerprint' });
  });

  it('points updates at the project once eas init has written an id', () => {
    const config = withEasUpdates({
      name: 'Stockbot',
      slug: 'stockbot',
      owner: 'rhionin',
      extra: { eas: { projectId: '11111111-2222-3333-4444-555555555555' } },
      runtimeVersion: { policy: 'appVersion' },
      updates: { url: 'https://example.invalid/updates' },
    });

    expect(config.owner).toBe('rhionin');
    expect(config.runtimeVersion).toEqual({ policy: 'fingerprint' });
    expect(config.updates).toMatchObject({
      enabled: true,
      checkAutomatically: 'ON_LOAD',
      fallbackToCacheTimeout: 0,
      url: 'https://u.expo.dev/11111111-2222-3333-4444-555555555555',
    });
  });
});
