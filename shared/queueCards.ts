// Copy for the two cards that sit above the batches in the scan queue: the QR
// misread and the in-flight product lookup. The web and the Expo app share it.

export const QR_CARD_TITLE = 'This was a QR code, not the barcode';
export const QR_RESCAN_LABEL = 'Rescan the barcode';
export const QR_DISMISS_LABEL = 'Dismiss';
export const QR_DISMISS_FAILURE = "Couldn't dismiss that scan.";

export const qrCardLabel = (preview: string): string =>
  preview === '' ? QR_CARD_TITLE : `${QR_CARD_TITLE}. ${preview}`;

export const PROCESSING_TITLE = 'Looking up product';
export const PROCESSING_DETAIL = 'Checking the open food database…';

export const processingBarcodeLine = (barcode: string): string => `Barcode: ${barcode}`;
export const processingCardLabel = (barcode: string): string => `Scan ${barcode} processing`;
