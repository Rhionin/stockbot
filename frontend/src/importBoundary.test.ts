import { readdirSync, readFileSync, statSync } from 'node:fs';
import { join, relative } from 'node:path';
import { describe, expect, it } from 'vitest';

const repoRoot = join(process.cwd(), '..');

const specifierPattern = /(?:\bfrom\s+|\bimport\s*\(\s*|\bimport\s+|\brequire\(\s*)(['"])([^'"]+)\1/g;

function specifiers(source: string): string[] {
  return [...source.matchAll(specifierPattern)].map((match) => match[2]);
}

// App trees may import @stockbot/shared and their own files. They may not
// import each other. shared/ is a leaf: it may not import either app.
export function importBoundaryViolation(home: 'frontend' | 'mobile' | 'shared', specifier: string): string | null {
  if (specifier === '@stockbot/shared' || specifier.startsWith('@stockbot/shared/')) return null;
  const mentions = (tree: 'frontend' | 'mobile') => new RegExp(`(?:^|/)${tree}(?:/|$)`).test(specifier);
  if (home === 'frontend' && mentions('mobile')) return `frontend imports the phone app via "${specifier}"`;
  if (home === 'mobile' && mentions('frontend')) return `mobile imports the web app via "${specifier}"`;
  if (home === 'shared' && (mentions('frontend') || mentions('mobile'))) {
    return `shared imports an app via "${specifier}"`;
  }
  return null;
}

function walk(dir: string, acc: string[] = []): string[] {
  for (const name of readdirSync(dir)) {
    if (name === 'node_modules' || name === 'dist' || name === 'coverage' || name === '.expo') continue;
    const full = join(dir, name);
    if (statSync(full).isDirectory()) walk(full, acc);
    else if (/\.(ts|tsx|js|mjs|cjs)$/.test(name)) acc.push(full);
  }
  return acc;
}

function homeOf(rel: string): 'frontend' | 'mobile' | 'shared' | null {
  if (rel.startsWith('frontend/')) return 'frontend';
  if (rel.startsWith('mobile/')) return 'mobile';
  if (rel.startsWith('shared/')) return 'shared';
  return null;
}

const enumDeclaration = /^\s*(?:export\s+)?(?:const\s+)?enum\s+/;
const namespaceDeclaration = /^\s*(?:export\s+)?namespace\s+/;

describe('import boundaries', () => {
  it('allows shared and rejects the other app', () => {
    expect(importBoundaryViolation('mobile', '@stockbot/shared/contract')).toBeNull();
    expect(importBoundaryViolation('frontend', '@stockbot/shared/types')).toBeNull();
    expect(importBoundaryViolation('frontend', '../../api/client')).toBeNull();
    expect(importBoundaryViolation('shared', './contract')).toBeNull();
    expect(importBoundaryViolation('mobile', '../../../frontend/src/api/contract')).toMatch(/web app/);
    expect(importBoundaryViolation('frontend', '../../../mobile/src/api/client')).toMatch(/phone app/);
    expect(importBoundaryViolation('shared', '../frontend/src/theme')).toMatch(/shared imports/);
    expect(importBoundaryViolation('shared', '../mobile/src/ui/theme')).toMatch(/shared imports/);
  });

  it('keeps frontend, mobile, and shared on their own sides of the door', () => {
    const violations: string[] = [];
    for (const tree of ['frontend', 'mobile', 'shared']) {
      for (const file of walk(join(repoRoot, tree))) {
        const rel = relative(repoRoot, file);
        const home = homeOf(rel);
        if (home === null) continue;
        const source = readFileSync(file, 'utf8');
        for (const specifier of specifiers(source)) {
          const violation = importBoundaryViolation(home, specifier);
          if (violation) violations.push(`${rel}: ${violation}`);
        }
      }
    }
    expect(violations).toEqual([]);
  });

  it('keeps shared free of enum and namespace declarations', () => {
    const violations: string[] = [];
    for (const file of walk(join(repoRoot, 'shared'))) {
      const lines = readFileSync(file, 'utf8').split('\n');
      lines.forEach((line, index) => {
        const trimmed = line.trim();
        if (trimmed.startsWith('//') || trimmed.startsWith('*') || trimmed.startsWith('/*')) return;
        if (enumDeclaration.test(line) || namespaceDeclaration.test(line)) {
          violations.push(`${relative(repoRoot, file)}:${index + 1}`);
        }
      });
    }
    expect(violations).toEqual([]);
  });
});
