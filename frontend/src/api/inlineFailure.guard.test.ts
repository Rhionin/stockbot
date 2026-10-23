import { existsSync, readdirSync, readFileSync } from 'node:fs';
import { join } from 'node:path';
import { describe, expect, it } from 'vitest';

function walk(dir: string, acc: string[] = []): string[] {
  if (!existsSync(dir)) return acc;
  for (const entry of readdirSync(dir, { withFileTypes: true })) {
    const full = join(dir, entry.name);
    if (entry.isDirectory()) walk(full, acc);
    else if (/\.(ts|tsx)$/.test(entry.name) && !/\.test\.tsx?$/.test(entry.name) && !/-test\.tsx?$/.test(entry.name)) {
      acc.push(full);
    }
  }
  return acc;
}

interface CatchSite {
  binding: string;
  body: string;
  at: number;
}

// Cheap scan of catch bodies. The behaviour table is the real guard; this only
// catches the common ways a caught value's message is read back out.
function catchSites(source: string): CatchSite[] {
  const sites: CatchSite[] = [];
  const heads = /catch\s*\(([^)]*)\)\s*\{|\.catch\s*\(\s*(?:\(([^)]*)\)|([A-Za-z_$][\w$]*))\s*=>\s*\{/g;
  let match: RegExpExecArray | null;
  while ((match = heads.exec(source)) !== null) {
    const binding = (match[1] ?? match[2] ?? match[3] ?? '').trim();
    let depth = 1;
    let index = heads.lastIndex;
    while (index < source.length && depth > 0) {
      const char = source[index];
      if (char === '{') depth += 1;
      else if (char === '}') depth -= 1;
      index += 1;
    }
    sites.push({ binding, body: source.slice(heads.lastIndex, index - 1), at: match.index });
  }
  return sites;
}

function bindingNames(binding: string): string[] {
  const withoutType = binding.replace(/:[^,)]*/g, '').trim();
  if (withoutType.startsWith('{')) return [];
  const name = withoutType.replace(/^\(|\)$/g, '').trim();
  return /^[A-Za-z_$][\w$]*$/.test(name) ? [name] : [];
}

function aliasesOf(body: string, names: string[]): string[] {
  const found = new Set(names);
  let grew = true;
  while (grew) {
    grew = false;
    for (const name of [...found]) {
      const alias = new RegExp(`(?:const|let|var)\\s+([A-Za-z_$][\\w$]*)\\s*=\\s*${name}\\b`, 'g');
      let match: RegExpExecArray | null;
      while ((match = alias.exec(body)) !== null) {
        if (!found.has(match[1])) {
          found.add(match[1]);
          grew = true;
        }
      }
    }
  }
  return [...found];
}

function messageHits(source: string): string[] {
  const hits: string[] = [];
  const lines = source.split('\n');
  const lineOf = (index: number) => source.slice(0, index).split('\n').length;
  for (const site of catchSites(source)) {
    const head = site.binding;
    if (/\{[^}]*\bmessage\b/.test(head)) {
      hits.push(`${lineOf(site.at)}: catch (${head})`);
    }
    const names = aliasesOf(site.body, bindingNames(head));
    for (const name of names) {
      const patterns = [
        new RegExp(`\\b${name}\\s*\\.\\s*message\\b`),
        new RegExp(`\\b${name}\\s*\\?\\.\\s*message\\b`),
        new RegExp(`\\b${name}\\s*\\[\\s*['"]message['"]\\s*\\]`),
        new RegExp(`\\(\\s*${name}\\s+as\\s+[\\w.]+\\s*\\)\\s*\\.\\s*message\\b`),
        new RegExp(`(?:const|let|var)\\s*\\{[^}]*\\bmessage\\b[^}]*\\}\\s*=\\s*${name}\\b`),
        new RegExp(`\\bString\\s*\\(\\s*${name}\\s*\\)`),
        new RegExp(`\\bJSON\\.stringify\\s*\\(\\s*${name}\\b`),
        new RegExp(`\`[^\`]*\\$\\{\\s*${name}\\b`),
      ];
      if (patterns.some((pattern) => pattern.test(site.body))) {
        const line = lines[lineOf(site.at) - 1]?.trim() ?? head;
        hits.push(`${lineOf(site.at)}: ${line}`);
      }
    }
    if (/instanceof Error \?/.test(site.body)) {
      hits.push(`${lineOf(site.at)}: instanceof Error ?`);
    }
  }
  return hits;
}

describe('inline failure sites', () => {
  it('does not read a caught error message outside inlineFailure', () => {
    const files = [
      ...walk('src'),
      ...walk('../mobile/src/ui'),
      ...walk('../mobile/src/auth'),
      ...walk('../mobile/src/screens'),
      '../mobile/src/session.tsx',
    ];
    const hits: string[] = [];
    for (const file of files) {
      const source = readFileSync(file, 'utf8')
        .replace(/\/\*[\s\S]*?\*\//g, '')
        .replace(/\/\/.*$/gm, '');
      for (const hit of messageHits(source)) hits.push(`${file}:${hit}`);
    }
    expect(hits).toEqual([]);
  });

  it('flags an alias and a stringified catch binding', () => {
    const source = `
      try { work(); } catch (requestError) {
        const err = requestError;
        console.log(err.message);
      }
      try { work(); } catch (requestError) {
        console.log(String(requestError));
      }
      try { work(); } catch (requestError) {
        console.log(\`\${requestError}\`);
      }
      try { work(); } catch (requestError) {
        console.log(JSON.stringify(requestError));
      }
      try { work(); } catch (requestError) {
        const message = inlineFailure(requestError, '');
        return message;
      }
    `;
    expect(messageHits(source)).toHaveLength(4);
  });
});
