import { describe, expect, it } from 'vitest';
import { extractInviteCode } from './inviteCode';

describe('extractInviteCode', () => {
  it('extracts a segment from invite paths', () => {
    expect(extractInviteCode('https://stockbot.rhionin.com/invite/START-9999')).toBe('START-9999');
    expect(extractInviteCode('/invite/ABCD-EFGH')).toBe('ABCD-EFGH');
  });

  it('reads code from query strings and strips fragments', () => {
    expect(extractInviteCode('https://app.example/join?code=JOIN-1234')).toBe('JOIN-1234');
    expect(extractInviteCode('PLAIN-CODE#ignored')).toBe('PLAIN-CODE');
    expect(extractInviteCode('PLAIN-CODE?foo=bar')).toBe('PLAIN-CODE');
  });

  it('does not throw on malformed percent-encoding', () => {
    expect(extractInviteCode('/invite/BAD%')).toBe('BAD%');
  });
});
