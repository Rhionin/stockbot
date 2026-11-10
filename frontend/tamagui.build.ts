import type { TamaguiBuildOptions } from 'tamagui'

export default {
  config: '../ui/src/tamagui.config.ts',
  components: ['tamagui', '@stockbot/ui'],
  // Vite 8 + compiler extraction ON crashes in the module runner (React CJS under ESM eval); runtime + config gen only.
  disableExtraction: true,
} satisfies TamaguiBuildOptions
