import {
  pairingScannerScreenFallback,
  setupSheetCutLineCopy,
  setupSheetExpiryHelpCopy,
  setupSheetExpiryLine,
  setupSheetPairingStepCopy,
  type WallBarcodeSettings,
} from '@stockbot/shared/contract';
import { KitPrintFooter } from './kitPrintFooter';
import { PrintableBarcode } from './printableBarcode';
import { SetupWallCardPanels } from './wallKitPrintSheet';

export function SetupKitPrintSheet({
  householdName,
  barcodeValue,
  expiresAt,
  wallSettings,
}: {
  householdName: string;
  barcodeValue: string;
  expiresAt: string;
  wallSettings: WallBarcodeSettings;
}) {
  const displayHousehold = householdName.trim() || 'your household';

  return (
    <article className="kit-print-sheet setup-kit-print-sheet">
      <header className="kit-print-header">
        <h1 className="kit-print-title">Connect your scanner</h1>
        <p className="kit-print-subtitle">For {displayHousehold}</p>
      </header>
      <p className="kit-print-body">{setupSheetPairingStepCopy()}</p>
      <PrintableBarcode value={barcodeValue} moduleMm={0.5} heightMm={20} />
      <p className="kit-print-expiry-line">{setupSheetExpiryLine(expiresAt)}</p>
      <p className="kit-print-body-sm">{setupSheetExpiryHelpCopy()}</p>
      <p className="kit-print-body-sm">{pairingScannerScreenFallback()}</p>
      <div className="kit-print-cutline" aria-hidden="true">
        <span>✂</span>
        <span className="kit-print-cutline-dash" />
      </div>
      <p className="kit-print-body-sm">{setupSheetCutLineCopy()}</p>
      <SetupWallCardPanels settings={wallSettings} layout="side-by-side" moduleMm={0.5} heightMm={18} />
      <KitPrintFooter printableName="Setup & wall sheet" />
    </article>
  );
}
