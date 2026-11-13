import {
  existsSync,
  lstatSync,
  mkdirSync,
  mkdtempSync,
  realpathSync,
  rmSync,
  symlinkSync,
  writeFileSync,
} from 'fs';
import { join, resolve } from 'path';
import { tmpdir } from 'os';
import * as fs from 'fs';
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

function expectLinkPointsAtMobileNodeModules(layout: { mobileDir: string; uiDir: string }) {
  const linkPath = join(layout.uiDir, 'node_modules');
  const mobileNodeModules = join(layout.mobileDir, 'node_modules');
  expect(realpathSync(linkPath)).toBe(realpathSync(mobileNodeModules));
}

describe('link-ui-node-modules', () => {
  let root = '';

  afterEach(() => {
    if (root !== '') rmSync(root, { recursive: true, force: true });
    root = '';
    jest.restoreAllMocks();
  });

  it('creates a fresh relative link to mobile node_modules', () => {
    const layout = layoutRoot();
    root = layout.root;
    const { linkPath, symlinkTarget } = syncUiNodeModules(layout);
    expect(linkPath).toBe(join(layout.uiDir, 'node_modules'));
    expect(existsSync(linkPath)).toBe(true);
    expect(lstatSync(linkPath).isSymbolicLink()).toBe(true);
    expect(symlinkTarget).toBe(join('..', 'mobile', 'node_modules'));
    expectLinkPointsAtMobileNodeModules(layout);
  });

  it('replaces an existing symlink idempotently', () => {
    const layout = layoutRoot();
    root = layout.root;
    syncUiNodeModules(layout);
    syncUiNodeModules(layout);
    expectLinkPointsAtMobileNodeModules(layout);
  });

  it('replaces a real ui/node_modules directory', () => {
    const layout = layoutRoot();
    root = layout.root;
    const realDir = join(layout.uiDir, 'node_modules');
    mkdirSync(realDir, { recursive: true });
    writeFileSync(join(realDir, 'stale'), 'stale');
    syncUiNodeModules(layout);
    expect(lstatSync(realDir).isSymbolicLink()).toBe(true);
    expectLinkPointsAtMobileNodeModules(layout);
  });

  it('replaces a dangling absolute symlink without following the target', () => {
    const layout = layoutRoot();
    root = layout.root;
    const linkPath = join(layout.uiDir, 'node_modules');
    const danglingTarget = join(layout.root, 'gone', 'node_modules');
    symlinkSync(danglingTarget, linkPath);
    expect(() => fs.existsSync(linkPath)).not.toThrow();
    expect(fs.existsSync(linkPath)).toBe(false);

    expect(() => syncUiNodeModules(layout)).not.toThrow();
    expect(lstatSync(linkPath).isSymbolicLink()).toBe(true);
    expectLinkPointsAtMobileNodeModules(layout);
  });

  it('uses a junction with an absolute target on Windows', () => {
    const layout = layoutRoot();
    root = layout.root;
    const symlinkSpy = jest.fn(fs.symlinkSync.bind(fs));
    const fsImpl = { ...fs, symlinkSync: symlinkSpy };
    const mobileNodeModules = resolve(join(layout.mobileDir, 'node_modules'));
    const linkPath = join(layout.uiDir, 'node_modules');

    syncUiNodeModules({ ...layout, platform: 'win32', fsImpl });

    expect(symlinkSpy).toHaveBeenCalledTimes(1);
    expect(symlinkSpy).toHaveBeenCalledWith(mobileNodeModules, linkPath, 'junction');
  });
});
