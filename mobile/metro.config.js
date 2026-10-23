const { getDefaultConfig } = require('expo/metro-config');
const path = require('path');

const config = getDefaultConfig(__dirname);

// The phone imports the shared contract at frontend/src/api/contract.ts, plus
// barcode formats and the scan cooldown. Jest resolves that same relative path.
config.watchFolders = [path.resolve(__dirname, '../frontend/src')];

// Babel helpers in that shared file are bare imports. Lookup starts at the
// file, which is outside this package, so also search this app's modules.
const mobileModules = path.resolve(__dirname, 'node_modules');
if (!config.resolver.nodeModulesPaths.includes(mobileModules)) {
  config.resolver.nodeModulesPaths.push(mobileModules);
}

module.exports = config;
