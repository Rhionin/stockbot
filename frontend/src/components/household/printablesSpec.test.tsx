import { render, screen } from '@testing-library/react';
import { MantineProvider } from '@mantine/core';
import { describe, expect, it } from 'vitest';
import { formatPairingCode } from '@stockbot/shared/contract';
import { readFileSync } from 'node:fs';
import { dirname, join } from 'node:path';
import { fileURLToPath } from 'node:url';
import { SetupKitPrintSheet } from './setupKitPrintSheet';
import { WallKitPrintSheet } from './wallKitPrintSheet';
import './kitPrint.css';

const MM_PER_CSS_PX = 25.4 / 96;
const MIN_X_MM = 0.375;
const PAIRING = formatPairingCode('23456789');

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
