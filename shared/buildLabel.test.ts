import { describe, expect, it } from 'vitest';
import fc from 'fast-check';
import { formatBuildLabel, formatCommitTime } from './buildLabel';

const ISO_TIME = /^(\d{4}-\d{2}-\d{2})[T ](\d{2}:\d{2})(?::\d{2}(?:\.\d+)?)?(Z|[+-]\d{2}:\d{2})?$/;

const pad = (value: number) => String(value).padStart(2, '0');

const isoTimeArbitrary = fc.tuple(
  fc.integer({ min: 1970, max: 2100 }),
  fc.integer({ min: 1, max: 12 }),
  fc.integer({ min: 1, max: 28 }),
  fc.integer({ min: 0, max: 23 }),
  fc.integer({ min: 0, max: 59 }),
  fc.integer({ min: 0, max: 59 }),
  fc.constantFrom('Z', '+00:00', '-07:00', '+05:30'),
).map(([year, month, day, hour, minute, second, zone]) =>
  `${year}-${pad(month)}-${pad(day)}T${pad(hour)}:${pad(minute)}:${pad(second)}${zone}`,
);

const expectedTime = (iso: string): string => {
  const match = ISO_TIME.exec(iso);
  if (!match) throw new Error(`not an ISO time: ${iso}`);
  const zone = match[3] === 'Z' || match[3] === '+00:00' ? 'UTC' : match[3];
  return `${match[1]} ${match[2]} ${zone}`;
};

describe('formatCommitTime', () => {
  it('renders ISO 8601 timestamps as a short date and zone', () => {
    fc.assert(
      fc.property(isoTimeArbitrary, (iso) => {
        expect(formatCommitTime(iso)).toBe(expectedTime(iso));
      }),
    );
  });

  it('keeps a non-ISO stamp visible and drops empty input', () => {
    fc.assert(
      fc.property(
        fc.string().filter((value) => value.trim() !== '' && ISO_TIME.exec(value.trim()) === null),
        (value) => {
          expect(formatCommitTime(value)).toBe(value.trim());
        },
      ),
    );
    expect(formatCommitTime('')).toBe('');
    expect(formatCommitTime('   ')).toBe('');
  });
});

describe('formatBuildLabel', () => {
  it('keeps the full commit and includes only the identity fields that are present', () => {
    fc.assert(
      fc.property(
        fc.string(),
        fc.string(),
        fc.string(),
        fc.string(),
        (commit, committedAt, subject, version) => {
          const label = formatBuildLabel({ commit, committedAt, subject, version });
          const collapsedSubject = subject.replace(/\s+/g, ' ').trim();
          const collapsedVersion = version.replace(/\s+/g, ' ').trim();
          const time = formatCommitTime(committedAt);
          if (commit === '' && collapsedSubject === '' && time === '' && collapsedVersion === '') {
            expect(label).toBeNull();
            return;
          }
          expect(label).not.toBeNull();
          if (commit !== '') {
            expect(label?.accessibleName.includes(`build ${commit}`)).toBe(true);
            expect(label?.detail.includes(commit)).toBe(true);
          }
          if (collapsedSubject !== '') {
            expect(label?.subject).toBe(collapsedSubject);
            expect(label?.accessibleName.startsWith(collapsedSubject)).toBe(true);
          } else {
            expect(label?.subject).toBe('');
          }
          if (time !== '') {
            expect(label?.detail.includes(time)).toBe(true);
            expect(label?.accessibleName.includes(time)).toBe(true);
          }
          if (collapsedVersion !== '') {
            expect(label?.version).toBe(collapsedVersion);
            expect(label?.accessibleName.includes(`version ${collapsedVersion}`)).toBe(true);
          } else {
            expect(label?.version).toBe('');
          }
          expect(label?.title).toBe(label?.accessibleName);
        },
      ),
    );
  });
});
