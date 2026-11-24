import { isOlderVersion } from './appVersion';

describe('isOlderVersion', () => {
  it.each([
    ['1.0.0', '1.0.1', true],
    ['1.9.0', '1.10.0', true],
    ['1.0', '1.0.0', false],
    ['2.0.0', '1.99.99', false],
    ['1.2.3', '1.2.3', false],
    ['1.2.3-beta.1', '1.2.4', true],
  ])('%s against minimum %s is %s', (version, minimum, expected) => {
    expect(isOlderVersion(version, minimum)).toBe(expected);
  });
});
