import { readFileSync } from 'node:fs';
import { join } from 'node:path';
import { describe, expect, it } from 'vitest';

describe('copyable barcode', () => {
  it('wraps a long barcode anywhere', () => {
    const css = readFileSync(join(process.cwd(), 'src/index.css'), 'utf8');
    const blocks = [...css.matchAll(/\.copyable-barcode\s*\{([^}]*)\}/g)].map((match) => match[1] ?? '');
    expect(blocks.length).toBeGreaterThan(0);
    for (const block of blocks) {
      expect(block).toMatch(/overflow-wrap:\s*anywhere\s*;/);
    }
  });
});
