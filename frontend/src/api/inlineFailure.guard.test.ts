import { readdirSync, readFileSync } from 'node:fs';
import { join } from 'node:path';
import { describe, expect, it } from 'vitest';

function walk(dir: string, acc: string[] = []): string[] {
  for (const entry of readdirSync(dir, { withFileTypes: true })) {
    const full = join(dir, entry.name);
    if (entry.isDirectory()) walk(full, acc);
    else if (/\.(ts|tsx)$/.test(entry.name) && !/\.test\.tsx?$/.test(entry.name) && !/-test\.tsx?$/.test(entry.name)) {
      acc.push(full);
    }
  }
  return acc;
}

// Handlers that show a failure have to go through inlineFailure. A direct
// error.message read puts the offline sentence in a card as well as the banner.
const caughtMessage = /instanceof Error \?|\b(?:requestError|error|err|reason)\.message\b/;

describe('inline failure sites', () => {
  it('does not read a caught error message outside inlineFailure', () => {
    const files = [
      ...walk('src/components'),
      ...walk('../mobile/src/screens'),
      '../mobile/src/session.tsx',
    ];
    const hits: string[] = [];
    for (const file of files) {
      readFileSync(file, 'utf8').split('\n').forEach((line, index) => {
        const code = line.replace(/\/\/.*$/, '');
        if (caughtMessage.test(code)) hits.push(`${file}:${index + 1}: ${line.trim()}`);
      });
    }
    expect(hits).toEqual([]);
  });
});
