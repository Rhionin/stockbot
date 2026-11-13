const fs = require('fs');
const path = require('path');

const RELATIVE_TARGET = path.join('..', 'mobile', 'node_modules');

function resolveDefaults() {
  const mobileDir = path.resolve(__dirname, '..');
  const uiDir = path.resolve(mobileDir, '..', 'ui');
  return { mobileDir, uiDir };
}

function removeUiNodeModules(linkPath, fsImpl = fs) {
  let stat;
  try {
    stat = fsImpl.lstatSync(linkPath);
  } catch (err) {
    if (err && err.code === 'ENOENT') return;
    throw err;
  }
  if (stat.isSymbolicLink()) {
    fsImpl.unlinkSync(linkPath);
    return;
  }
  if (stat.isDirectory()) {
    fsImpl.rmSync(linkPath, { recursive: true, force: true });
    return;
  }
  fsImpl.unlinkSync(linkPath);
}

function syncUiNodeModules(options = {}) {
  const {
    mobileDir,
    uiDir,
    platform = process.platform,
    fsImpl = fs,
  } = { ...resolveDefaults(), ...options };
  const linkPath = path.join(uiDir, 'node_modules');
  const mobileNodeModules = path.join(mobileDir, 'node_modules');
  removeUiNodeModules(linkPath, fsImpl);
  const linkType = platform === 'win32' ? 'junction' : 'dir';
  const symlinkTarget = linkType === 'junction'
    ? path.resolve(mobileNodeModules)
    : path.relative(uiDir, mobileNodeModules);
  fsImpl.symlinkSync(symlinkTarget, linkPath, linkType);
  return { linkPath, linkType, symlinkTarget, mobileNodeModules };
}

if (require.main === module) {
  syncUiNodeModules();
}

module.exports = { syncUiNodeModules, removeUiNodeModules, RELATIVE_TARGET };
