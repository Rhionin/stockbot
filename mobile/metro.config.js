const { getDefaultConfig } = require('expo/metro-config');
const path = require('path');

const config = getDefaultConfig(__dirname);

// The phone imports the shared contract at frontend/src/api/contract.ts, plus
// barcode formats and the scan cooldown. Jest resolves that same relative path.
config.watchFolders = [path.resolve(__dirname, '../frontend/src')];

module.exports = config;
