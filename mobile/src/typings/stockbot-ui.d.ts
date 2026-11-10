declare module '@stockbot/ui' {
  import type { ReactNode } from 'react';

  export const TAMAGUI_NOTICE_TEST_ID: string;
  export const TAMAGUI_NOTICE_ACTION_LABEL: string;

  export type TamaguiNoticeBannerProps = {
    message?: string;
    actionLabel?: string;
    onAction?: () => void;
  };

  export function TamaguiNoticeBanner(props: TamaguiNoticeBannerProps): ReactNode;

  export function TamaguiSpikeProvider(props: { children: ReactNode; theme?: 'light' | 'dark' }): ReactNode;
}
