const { getDefaultConfig } = require('expo/metro-config');
const path = require('path');

const projectRoot = __dirname;
const sharedRoot = path.resolve(projectRoot, '../shared');
const uiRoot = path.resolve(projectRoot, '../ui');

const config = getDefaultConfig(projectRoot);

// Shared modules live outside this package. Watch them, and resolve bare
// imports (including Babel helpers injected into those files) from this app.
config.watchFolders = [sharedRoot, uiRoot];
config.resolver.nodeModulesPaths = [path.resolve(projectRoot, 'node_modules')];

const sharedPrefix = '@stockbot/shared/';
const uiPrefix = '@stockbot/ui';
config.resolver.resolveRequest = (context, moduleName, platform) => {
  if (moduleName.startsWith(sharedPrefix)) {
    const target = path.join(sharedRoot, moduleName.slice(sharedPrefix.length));
    return context.resolveRequest(context, target, platform);
  }
  if (moduleName === uiPrefix || moduleName.startsWith(`${uiPrefix}/`)) {
    const sub = moduleName === uiPrefix ? 'src/index.ts' : `src/${moduleName.slice(uiPrefix.length + 1)}`;
    const target = path.join(uiRoot, sub);
    return context.resolveRequest(context, target, platform);
  }
  return context.resolveRequest(context, moduleName, platform);
};

module.exports = config;
