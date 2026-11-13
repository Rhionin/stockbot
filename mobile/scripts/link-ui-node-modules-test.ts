import { existsSync, lstatSync, mkdirSync, mkdtempSync, readlinkSync, rmSync, writeFileSync } from 'fs';
import { join } from 'path';
import { tmpdir } from 'os';
import { syncUiNodeModules } from './link-ui-node-modules.cjs';

function layoutRoot() {
  const root = mkdtempSync(join(tmpdir(), 'stockbot-ui-link-'));
  const mobileDir = join(root, 'mobile');
  const uiDir = join(root, 'ui');
  mkdirSync(join(mobileDir, 'node_modules'), { recursive: true });
  mkdirSync(uiDir, { recursive: true });
  writeFileSync(join(mobileDir, 'node_modules', '.keep'), '');
  return { root, mobileDir, uiDir };
}

describe('link-ui-node-modules', () => {
  let root = '';

  afterEach(() => {
    if (root !== '') rmSync(root, { recursive: true, force: true });
    root = '';
  });

  it('creates a fresh relative link to mobile node_modules', () => {
    const layout = layoutRoot();
    root = layout.root;
    const { linkPath, target } = syncUiNodeModules(layout);
    expect(linkPath).toBe(join(layout.uiDir, 'node_modules'));
    expect(existsSync(linkPath)).toBe(true);
    expect(lstatSync(linkPath).isSymbolicLink()).toBe(true);
    expect(readlinkSync(linkPath)).toBe(target);
    expect(target).toBe(join('..', 'mobile', 'node_modules'));
  });

  it('replaces an existing symlink idempotently', () => {
    const layout = layoutRoot();
    root = layout.root;
    syncUiNodeModules(layout);
    syncUiNodeModules(layout);
    expect(readlinkSync(join(layout.uiDir, 'node_modules'))).toBe(join('..', 'mobile', 'node_modules'));
  });

  it('replaces a real ui/node_modules directory', () => {
    const layout = layoutRoot();
    root = layout.root;
    const realDir = join(layout.uiDir, 'node_modules');
    mkdirSync(realDir, { recursive: true });
    writeFileSync(join(realDir, 'stale'), 'stale');
    syncUiNodeModules(layout);
    expect(lstatSync(realDir).isSymbolicLink()).toBe(true);
    expect(readlinkSync(realDir)).toBe(join('..', 'mobile', 'node_modules'));
  });
});
