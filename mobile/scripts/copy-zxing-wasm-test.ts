import { mkdtempSync, readFileSync, rmSync } from 'fs';
import { createHash } from 'crypto';
import { join } from 'path';
import { tmpdir } from 'os';
import * as fs from 'fs';
import { copyZxingWasm, PUBLIC_WASM_PATH } from './copy-zxing-wasm.cjs';

describe('copy-zxing-wasm', () => {
  let root = '';

  beforeEach(() => {
    root = mkdtempSync(join(tmpdir(), 'stockbot-zxing-'));
  });

  afterEach(() => {
    rmSync(root, { recursive: true, force: true });
  });

  it('copies the reader the detector expects into public/', () => {
    const result = copyZxingWasm({ mobileDir: root });

    expect(result.target).toBe(join(root, 'public', PUBLIC_WASM_PATH));
    const copied = readFileSync(result.target);
    expect(createHash('sha256').update(copied).digest('hex')).toBe(result.sha256);
    expect(Array.from(copied.subarray(0, 4))).toEqual([0, 0x61, 0x73, 0x6d]);
  });

  it('fails the build when the installed file is not the build the detector pins', () => {
    const other = { ...fs, readFileSync: () => Buffer.from('not the reader') } as unknown as typeof fs;

    expect(() => copyZxingWasm({ mobileDir: root, fsImpl: other })).toThrow(/expects/);
    expect(fs.existsSync(join(root, 'public'))).toBe(false);
  });
});
