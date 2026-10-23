import js from '@eslint/js';
import cdkPlugin from 'eslint-plugin-awscdk';
import eslintConfigPrettier from 'eslint-config-prettier';
import { defineConfig, globalIgnores } from 'eslint/config';
import tseslint from 'typescript-eslint';

export default defineConfig([
  globalIgnores(['node_modules/**', 'cdk.out/**', 'dist/**']),
  {
    files: ['bin/**/*.ts', 'lib/**/*.ts', 'test/**/*.ts'],
    extends: [
      js.configs.recommended,
      tseslint.configs.recommended,
      cdkPlugin.configs.recommended,
      eslintConfigPrettier,
    ],
  },
]);
