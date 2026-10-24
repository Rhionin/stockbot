const { getDefaultConfig } = require('expo/metro-config');
const path = require('path');

const projectRoot = __dirname;
const sharedRoot = path.resolve(projectRoot, '../shared');

const config = getDefaultConfig(projectRoot);

// Shared modules live outside this package. Watch them, and resolve bare
// imports (including Babel helpers injected into those files) from this app.
config.watchFolders = [sharedRoot];
config.resolver.nodeModulesPaths = [path.resolve(projectRoot, 'node_modules')];

const sharedPrefix = '@stockbot/shared/';
config.resolver.resolveRequest = (context, moduleName, platform) => {
  if (moduleName.startsWith(sharedPrefix)) {
    const target = path.join(sharedRoot, moduleName.slice(sharedPrefix.length));
    return context.resolveRequest(context, target, platform);
  }
  return context.resolveRequest(context, moduleName, platform);
};

module.exports = config;
