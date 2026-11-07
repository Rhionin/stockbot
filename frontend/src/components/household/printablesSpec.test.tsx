import { render, screen } from '@testing-library/react';
import { MantineProvider } from '@mantine/core';
import { describe, expect, it } from 'vitest';
import { formatPairingCode } from '@stockbot/shared/contract';
import { readFileSync } from 'node:fs';
import { dirname, join } from 'node:path';
import { fileURLToPath } from 'node:url';
import { PAIRING_SCANNER_SCREEN_FALLBACK, pairingScannerScreenFallback } from './kitCopy456';
import { SetupKitPrintSheet } from './setupKitPrintSheet';
import { WallKitPrintSheet } from './wallKitPrintSheet';
import './kitPrint.css';

const MM_PER_CSS_PX = 25.4 / 96;
const MIN_X_MM = 0.375;
const PAIRING = formatPairingCode('23456789');

function setupSheetCssPt(css: string, classSuffix: string, property = 'font-size'): number {
  const re = new RegExp(
    `\\.setup-kit-print-sheet\\s+\\.${classSuffix.replace(/-/g, '\\-')}[^{]*\\{([^}]+)\\}`,
    's',
  );
  const block = css.match(re)?.[1] ?? '';
  const match = block.match(new RegExp(`${property}:\\s*([\\d.]+)pt`));
  expect(match, `missing ${property} for .setup-kit-print-sheet .${classSuffix}`).not.toBeNull();
  return Number(match![1]);
}

function assertAtPageMargin12_7mm(css: string) {
  const pageRule = css.match(/@page\s*\{([^}]+)\}/s)?.[1] ?? '';
  expect(pageRule).toMatch(/margin:\s*12\.7mm/);
}

function assertBarcodeSpec(root: ParentNode) {
  for (const svg of root.querySelectorAll('svg[data-barcode]')) {
    const barModules = Number(svg.getAttribute('data-modules'));
    const totalModules = Number(svg.getAttribute('data-total-modules') ?? barModules);
    const widthAttr = svg.getAttribute('width')!;
    const widthMm = widthAttr.endsWith('mm') ? parseFloat(widthAttr) : parseFloat(widthAttr) * MM_PER_CSS_PX;
    expect(widthMm / totalModules).toBeGreaterThanOrEqual(MIN_X_MM);
    const block = svg.closest('.printable-barcode-block');
    expect(block).not.toBeNull();
    const moduleMm = Number(block!.getAttribute('data-module-mm'));
    const quietMm = Number(block!.getAttribute('data-quiet-mm'));
    expect(quietMm).toBeGreaterThanOrEqual(Math.max(6, 10 * moduleMm));
    const heightAttr = svg.getAttribute('height')!;
    const heightMm = heightAttr.endsWith('mm') ? parseFloat(heightAttr) : parseFloat(heightAttr) * MM_PER_CSS_PX;
    expect(heightMm).toBeGreaterThanOrEqual(15);
    expect(svg.getAttribute('shape-rendering')).toBe('crispEdges');
    expect([...svg.querySelectorAll('rect')].every((rect) => rect.getAttribute('fill') === '#000000')).toBe(true);
    expect(svg.nextElementSibling?.textContent).toBe(svg.getAttribute('data-value'));
    const firstRect = svg.querySelector('rect');
    expect(Number(firstRect?.getAttribute('x'))).toBeGreaterThanOrEqual(10);
  }
  expect(root.querySelector('.pairing-qr, [aria-label^="QR code"]')).toBeNull();
}

describe('kit printables spec (#454 / #456 B16–B18)', () => {
  it('setup sheet barcodes meet module sizing and omit QR / Setup B', () => {
    const { container } = render(
      <MantineProvider env="test">
        <div className="kit-print-root" data-printing="true">
          <SetupKitPrintSheet
            householdName="Test Household"
            barcodeValue={PAIRING}
            expiresAt="2099-06-15T14:05:00.000Z"
            wallSettings={{ inactivityRevertSeconds: 300, defaultDirection: 'stock_out' }}
          />
        </div>
      </MantineProvider>,
    );
    const root = container.querySelector('.kit-print-root')!;
    assertBarcodeSpec(root);
    expect(root.textContent).not.toMatch(/%%SpecCode99|Symcode|Setup B/);
    expect(screen.getByText(/Connect your scanner/)).toBeVisible();
    expect(root.querySelector('.kit-print-ruler-caption')).not.toBeNull();
  });

  it('draws the print ruler with a border line, not a background fill', () => {
    const cssPath = join(dirname(fileURLToPath(import.meta.url)), 'kitPrint.css');
    const css = readFileSync(cssPath, 'utf8');
    const screenRule = css.match(/\.kit-print-ruler\s*\{([^}]+)\}/)?.[1] ?? '';
    expect(screenRule).toMatch(/border-top:\s*2px\s+solid/);
    expect(screenRule).not.toMatch(/background:\s*(?!none)/);
  });

  it('#556 fallback on setup sheet is spec §2a exact text at ≥11pt in CSS', () => {
    expect(pairingScannerScreenFallback()).toBe(PAIRING_SCANNER_SCREEN_FALLBACK);
    const cssPath = join(dirname(fileURLToPath(import.meta.url)), 'kitPrint.css');
    const css = readFileSync(cssPath, 'utf8');
    expect(setupSheetCssPt(css, 'kit-print-body-sm')).toBeGreaterThanOrEqual(11);
    const { container } = render(
      <MantineProvider env="test">
        <div className="kit-print-root" data-printing="true">
          <SetupKitPrintSheet
            householdName="Synthetic Household"
            barcodeValue={PAIRING}
            expiresAt="2099-06-15T14:05:00.000Z"
            wallSettings={{ inactivityRevertSeconds: 300, defaultDirection: 'stock_out' }}
          />
        </div>
      </MantineProvider>,
    );
    expect(container.textContent).toContain(PAIRING_SCANNER_SCREEN_FALLBACK);
  });

  it('#556 setup sheet CSS meets printables §1.1 minimums (body, mode, barcode caption, margins)', () => {
    const cssPath = join(dirname(fileURLToPath(import.meta.url)), 'kitPrint.css');
    const css = readFileSync(cssPath, 'utf8');
    assertAtPageMargin12_7mm(css);
    expect(setupSheetCssPt(css, 'kit-print-body-sm')).toBeGreaterThanOrEqual(11);
    expect(setupSheetCssPt(css, 'kit-wall-panel-copy')).toBeGreaterThanOrEqual(11);
    expect(setupSheetCssPt(css, 'printable-barcode-caption')).toBeGreaterThanOrEqual(12);
    expect(setupSheetCssPt(css, 'kit-wall-mode-word')).toBeGreaterThanOrEqual(28);
    const modeBlock = css.match(/\.setup-kit-print-sheet\s+\.kit-wall-mode-word\s*\{([^}]+)\}/s)?.[1] ?? '';
    expect(modeBlock).toMatch(/font-weight:\s*700/);
    const captionBlock = css.match(/\.setup-kit-print-sheet\s+\.printable-barcode-caption\s*\{([^}]+)\}/s)?.[1] ?? '';
    expect(captionBlock).toMatch(/monospace/);
    for (const suffix of ['kit-print-footer-instruction', 'kit-print-footer-meta', 'kit-print-ruler-caption'] as const) {
      expect(setupSheetCssPt(css, suffix)).toBeGreaterThanOrEqual(11);
    }
  });

  it('#556 C-7: default Stock in places Goes back on the Stock out panel', () => {
    const { container } = render(
      <MantineProvider env="test">
        <div className="kit-print-root" data-printing="true">
          <SetupKitPrintSheet
            householdName="Synthetic Household"
            barcodeValue={PAIRING}
            expiresAt="2099-06-15T14:05:00.000Z"
            wallSettings={{ inactivityRevertSeconds: 300, defaultDirection: 'stock_in' }}
          />
        </div>
      </MantineProvider>,
    );
    const outPanel = container.querySelector('.kit-wall-panel-out');
    const inPanel = container.querySelector('.kit-wall-panel-in');
    expect(outPanel?.textContent).toMatch(/Goes back to Stock in/);
    expect(inPanel?.textContent).not.toMatch(/Goes back to/);
    expect(inPanel?.textContent).toMatch(/This is the normal mode/);
  });

  it('standalone wall sheet barcodes meet module sizing', () => {
    const { container } = render(
      <MantineProvider env="test">
        <div className="kit-print-root" data-printing="true">
          <WallKitPrintSheet settings={{ inactivityRevertSeconds: 180, defaultDirection: 'stock_out' }} />
        </div>
      </MantineProvider>,
    );
    assertBarcodeSpec(container.querySelector('.kit-print-root')!);
  });
});
