import { describeBuild } from './buildStamp';

describe('describeBuild', () => {
  it('reports the installed version and update id from a preview build', () => {
    expect(describeBuild({
      nativeApplicationVersion: '1.0.0',
      nativeBuildVersion: '4',
      configVersion: '1.0.0',
      updateId: '11111111-2222-3333-4444-555555555555',
      runtimeVersion: 'fingerprint-abc',
      updatesEnabled: true,
    })).toEqual({
      version: '1.0.0 (4)',
      updateId: '11111111-2222-3333-4444-555555555555',
      runtimeVersion: 'fingerprint-abc',
    });
  });

  it('calls the copy inside the APK embedded when no update has been downloaded', () => {
    expect(describeBuild({
      nativeApplicationVersion: '1.0.0',
      nativeBuildVersion: '1',
      configVersion: '9.9.9',
      updateId: null,
      runtimeVersion: '   ',
      updatesEnabled: true,
    })).toEqual({
      version: '1.0.0 (1)',
      updateId: 'embedded',
      runtimeVersion: 'unavailable',
    });
  });

  it('uses the app config version in Expo Go, where expo-updates is off', () => {
    expect(describeBuild({
      nativeApplicationVersion: '58.0.0',
      nativeBuildVersion: '58',
      configVersion: '1.0.0',
      updateId: null,
      runtimeVersion: null,
      updatesEnabled: false,
    })).toEqual({
      version: '1.0.0',
      updateId: 'embedded',
      runtimeVersion: 'unavailable',
    });
  });
});
