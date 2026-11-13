const fs = require('fs');
const path = require('path');

const RELATIVE_TARGET = path.join('..', 'mobile', 'node_modules');

function resolveDefaults() {
  const mobileDir = path.resolve(__dirname, '..');
  const uiDir = path.resolve(mobileDir, '..', 'ui');
  return { mobileDir, uiDir };
}

function removeUiNodeModules(linkPath) {
  if (!fs.existsSync(linkPath)) return;
  const stat = fs.lstatSync(linkPath);
  if (stat.isSymbolicLink() || stat.isDirectory()) {
    fs.rmSync(linkPath, { recursive: true, force: true });
    return;
  }
  fs.unlinkSync(linkPath);
}

function syncUiNodeModules(options = {}) {
  const { mobileDir, uiDir } = { ...resolveDefaults(), ...options };
  const linkPath = path.join(uiDir, 'node_modules');
  const mobileNodeModules = path.join(mobileDir, 'node_modules');
  removeUiNodeModules(linkPath);
  const linkType = process.platform === 'win32' ? 'junction' : 'dir';
  const target = linkType === 'junction'
    ? mobileNodeModules
    : path.relative(uiDir, mobileNodeModules);
  fs.symlinkSync(target, linkPath, linkType);
  return { linkPath, target: linkType === 'junction' ? path.relative(uiDir, mobileNodeModules) : target };
}

if (require.main === module) {
  syncUiNodeModules();
}

module.exports = { syncUiNodeModules, removeUiNodeModules, RELATIVE_TARGET };
