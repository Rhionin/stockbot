import {
  formatInactivityDuration,
  stockDirectionLabel,
  type WallBarcodeSettings,
} from '@stockbot/shared/contract';
import { PrintableBarcode } from './printableBarcode';
import { KitPrintFooter } from './kitPrintFooter';

function stockOutBody(): string {
  return 'Scan before you take things out. This is the normal mode.';
}

function stockInBody(settings: WallBarcodeSettings): string {
  const duration = formatInactivityDuration(settings.inactivityRevertSeconds);
  const revert = stockDirectionLabel(settings.defaultDirection);
  return `Scan before you put groceries away. Goes back to ${revert} ${duration} after your last scan.`;
}

export function SetupWallCardPanels({
  settings,
  layout,
  moduleMm,
  heightMm,
}: {
  settings: WallBarcodeSettings;
  layout: 'side-by-side' | 'stacked';
  moduleMm: number;
  heightMm: number;
}) {
  const gridClass = layout === 'side-by-side' ? 'kit-wall-panels kit-wall-panels-row' : 'kit-wall-panels kit-wall-panels-stack';

  return (
    <div className={gridClass}>
      <section className="kit-wall-panel kit-wall-panel-out">
        <p className="kit-wall-mode-word">
          ↑ STOCK OUT
          {settings.defaultDirection === 'stock_out' && <span className="kit-wall-default-badge">DEFAULT</span>}
        </p>
        <h2 className="kit-wall-panel-title">Taking things off the shelf</h2>
        <PrintableBarcode value="STOCK-OUT" moduleMm={moduleMm} heightMm={heightMm} />
        <p className="kit-wall-panel-copy">{stockOutBody()}</p>
      </section>
      <section className="kit-wall-panel kit-wall-panel-in">
        <p className="kit-wall-mode-word">
          ↓ STOCK IN
          {settings.defaultDirection === 'stock_in' && <span className="kit-wall-default-badge kit-wall-default-badge-in">DEFAULT</span>}
        </p>
        <h2 className="kit-wall-panel-title">Putting groceries away</h2>
        <PrintableBarcode value="STOCK-IN" moduleMm={moduleMm} heightMm={heightMm} />
        <p className="kit-wall-panel-copy">{stockInBody(settings)}</p>
      </section>
    </div>
  );
}

export function WallKitPrintSheet({
  settings,
  scannerName,
}: {
  settings: WallBarcodeSettings;
  scannerName?: string;
}) {
  return (
    <article className="kit-print-sheet wall-kit-print-sheet">
      <header className="kit-print-header">
        <h1 className="kit-print-title">Scanner mode barcodes</h1>
        <p className="kit-print-subtitle">
          Tape this next to your scanner. Scan a barcode to switch modes.
        </p>
        {scannerName && <p className="kit-print-subtitle">For {scannerName}</p>}
      </header>
      <SetupWallCardPanels settings={settings} layout="stacked" moduleMm={0.6} heightMm={22} />
      <p className="kit-print-body-sm kit-print-tip">
        Tip: you can also press the mode button on the scanner to switch.
      </p>
      <KitPrintFooter printableName="Wall barcodes" />
    </article>
  );
}
