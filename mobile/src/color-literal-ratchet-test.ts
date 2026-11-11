import { existsSync, readdirSync, readFileSync } from 'fs';
import { join, relative } from 'path';

const repoRoot = join(__dirname, '../..');

const ALLOWED_LITERAL_FILES = new Set([
  'mobile/src/ui/theme.ts',
  'shared/brandTokens.ts',
  'shared/contrast.ts',
  'shared/noticeBanner.ts',
]);

function walk(dir: string, acc: string[] = []): string[] {
  if (!existsSync(dir)) return acc;
  for (const entry of readdirSync(dir, { withFileTypes: true })) {
    const full = join(dir, entry.name);
    if (entry.isDirectory()) {
      if (entry.name === 'node_modules' || entry.name === 'dist') continue;
      walk(full, acc);
    } else if (/\.(ts|tsx)$/.test(entry.name) && !/\.test\.tsx?$/.test(entry.name) && !/-test\.tsx?$/.test(entry.name)) {
      acc.push(full);
    }
  }
  return acc;
}

function stripComments(source: string): string {
  return source
    .replace(/\/\*[\s\S]*?\*\//g, '')
    .replace(/(^|[^:])\/\/.*$/gm, '$1');
}

function hasColorLiteral(source: string): boolean {
  return /#[0-9a-fA-F]{3,8}\b/.test(source) || /rgba?\(\s*\d/.test(source);
}

describe('color literal ratchet', () => {
  it('keeps hex and rgb/rgba( literals out of ui/ except shared imports', () => {
    const violations: string[] = [];
    for (const file of walk(join(repoRoot, 'ui/src'))) {
      const rel = relative(repoRoot, file);
      const source = stripComments(readFileSync(file, 'utf8'));
      if (hasColorLiteral(source)) {
        violations.push(`${rel}: raw color literal`);
      }
    }
    expect(violations).toEqual([]);
  });

  it('allows mobile theme literals only in the pinned allowlist', () => {
    const violations: string[] = [];
    for (const tree of ['mobile/src/ui', 'shared']) {
      for (const file of walk(join(repoRoot, tree))) {
        const rel = relative(repoRoot, file);
        if (ALLOWED_LITERAL_FILES.has(rel)) continue;
        const source = stripComments(readFileSync(file, 'utf8'));
        if (hasColorLiteral(source)) {
          violations.push(`${rel}: unexpected color literal`);
        }
      }
    }
    expect(violations).toEqual([]);
  });
});
