/**
 * Jest environment polyfills for Tamagui + Expo (not a Tamagui API shim).
 * - __DEV__: Tamagui and RN dev paths read this global (see Tamagui polyfill-dev).
 */
const devGlobal = globalThis as typeof globalThis & { __DEV__?: boolean };

if (typeof devGlobal.__DEV__ === 'undefined') {
  devGlobal.__DEV__ = false;
}
