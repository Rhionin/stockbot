declare const global: { __DEV__?: boolean };

if (global.__DEV__ === undefined) {
  global.__DEV__ = true;
}
