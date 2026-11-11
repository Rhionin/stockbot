declare module '@stockbot/ui' {
  import type { ReactNode } from 'react';

  export const NOTICE_BANNER_TEST_ID: string;
  export const NOTICE_DEMO_ACTION_LABEL: string;
  export const NOTICE_SCREEN_INSET: number;
  export const QUEUE_CARD_TEST_ID_PREFIX: string;

  export type NoticeBannerProps = {
    topInset?: number;
    text?: string;
    fontRegular?: string;
    fontBold?: string;
    onDemoAction?: () => void;
    demoActionLabel?: string;
  };

  export function NoticeBanner(props: NoticeBannerProps): ReactNode;

  export type QueueCardProps = {
    entryId: string;
    label: string;
    barcode: string;
    metaLine: string;
    stockIn: boolean;
    stockOut: boolean;
    blocked: boolean;
    busy: boolean;
    busyThis: boolean;
    pinLive?: boolean;
    note?: string;
    fontRegular: string;
    fontBold: string;
    onConfirm: () => void;
    onRemove: () => void;
    footer?: ReactNode;
  };

  export function QueueCard(props: QueueCardProps): ReactNode;

  export function StockbotTamaguiProvider(props: { children: ReactNode; theme?: 'light' | 'dark' }): ReactNode;

  export const TOKEN_PALETTE: {
    colors: Set<string>;
    space: Set<number>;
    radius: Set<number>;
  };
  export const themes: Record<string, Record<string, string>>;
  export function paletteColorValues(): readonly string[];
}
