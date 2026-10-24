// ESLint ignores files outside the config file's directory. shared/ has no
// config of its own (it must not import either app), so this root config is
// what `eslint ../shared` loads, with the repo as its base path.
import { sharedLintConfig } from './frontend/eslint.config.js'

export default sharedLintConfig
