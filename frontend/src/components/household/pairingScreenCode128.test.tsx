import { render, screen, waitFor, within } from '@testing-library/react';
import { MantineProvider } from '@mantine/core';
import { afterEach, describe, expect, it, vi } from 'vitest';
import { formatPairingCode } from '@stockbot/shared/contract';
import { code128Bars } from './code128';
import {
  PAIRING_CODE128_MIN_MODULE_DEVICE_PX,
  PAIRING_CODE128_QUIET_MODULES,
  computePairingCode128Layout,
  pairingCode128BarRuns,
  pairingCode128ModuleCount,
  pairingCode128SvgWidthPx,
} from './pairingScreenCode128';
import { PrintableBarcode } from './printableBarcode';
import { printableBarcodeQuietMm } from './printableBarcodeQuiet';
import { Code128Mark } from './symbols';
import { PairingDialog } from './PairingDialog';
import { PAIRING_ON_SCREEN_TOO_SMALL_HINT } from './kitCopy456';

const PAIRING = formatPairingCode('23456789');
const BAR_MODULES = pairingCode128ModuleCount(PAIRING);

function expectedBarRuns(value: string): Array<{ x: number; width: number }> {
  const bars = code128Bars(value);
  const runs: Array<{ x: number; width: number }> = [];
  let index = 0;
  while (index < bars.length) {
    if (!bars[index]) {
      index += 1;
      continue;
    }
    const start = index;
    while (index < bars.length && bars[index]) index += 1;
    runs.push({ x: start + PAIRING_CODE128_QUIET_MODULES, width: index - start });
  }
  return runs;
}

function rectSequence(root: ParentNode) {
  const svg = root.querySelector('svg')!;
  return [...svg.querySelectorAll('rect')].map((rect) => ({
    x: Number(rect.getAttribute('x')),
    width: Number(rect.getAttribute('width')),
  }));
}

describe('pairingCode128 layout math', () => {
  it('pairingCode128SvgWidthPx scales total modules by css module width', () => {
    expect(pairingCode128SvgWidthPx(2, BAR_MODULES)).toBe((BAR_MODULES + 2 * PAIRING_CODE128_QUIET_MODULES) * 2);
  });

  it('computePairingCode128Layout uses integer device px and allows 1px only when 2 cannot fit', () => {
    const narrow = computePairingCode128Layout(366, BAR_MODULES, 3);
    expect(narrow.moduleDevicePx).toBe(4);
    expect(Number.isInteger(narrow.moduleDevicePx)).toBe(true);

    const desktop = computePairingCode128Layout(517, BAR_MODULES, 1);
    expect(desktop.moduleDevicePx).toBe(2);
    expect(desktop.svgWidthCssPx).toBe(440);

    const phone = computePairingCode128Layout(320, BAR_MODULES, 1);
    expect(phone.moduleDevicePx).toBe(1);
    expect(phone.svgWidthCssPx).toBeCloseTo(220, 5);

    const fractionalDpr = computePairingCode128Layout(292, BAR_MODULES, 1.5);
    expect(fractionalDpr.moduleDevicePx).toBeGreaterThanOrEqual(2);
    expect(fractionalDpr.moduleDevicePx).not.toBe(1);

    const highDprPhone = computePairingCode128Layout(320, BAR_MODULES, 2.625);
    expect(highDprPhone.moduleDevicePx).toBeGreaterThanOrEqual(2);
    expect(highDprPhone.svgWidthCssPx).toBeLessThanOrEqual(320 + 0.01);
  });
});

describe('on-screen Code128Mark geometry', () => {
  afterEach(() => {
    vi.unstubAllGlobals();
  });

  it('renders bar runs with quiet-zone offset matching the encoder', async () => {
    vi.stubGlobal('devicePixelRatio', 2);
    const { container } = render(
      <MantineProvider env="test">
        <div style={{ width: 360 }}>
          <Code128Mark value={PAIRING} quietZone />
        </div>
      </MantineProvider>,
    );
    await waitFor(() => {
      expect(container.querySelector('svg.pairing-code128 rect')).not.toBeNull();
    });
    expect(rectSequence(container)).toEqual(expectedBarRuns(PAIRING));
    const svg = container.querySelector('svg.pairing-code128')!;
    const moduleDevicePx = Number(svg.getAttribute('data-module-device-px'));
    expect(moduleDevicePx).toBeGreaterThanOrEqual(PAIRING_CODE128_MIN_MODULE_DEVICE_PX);
    expect(Number.isInteger(moduleDevicePx)).toBe(true);
    expect(Number(svg.getAttribute('width'))).toBeCloseTo(
      pairingCode128SvgWidthPx(Number(svg.getAttribute('data-module-css-px')), BAR_MODULES),
      5,
    );
    expect(svg.getAttribute('shape-rendering')).toBe('crispEdges');
    expect(container.querySelector('.pairing-barcode-too-small-hint')).toBeNull();
  });

  it('shows the print hint only when the module width is 1 device px', async () => {
    vi.stubGlobal('devicePixelRatio', 1);
    const clientWidthSpy = vi.spyOn(HTMLDivElement.prototype, 'clientWidth', 'get').mockImplementation(function clientWidth(this: HTMLDivElement) {
      if (this.classList.contains('pairing-code128-frame')) return 320;
      if (this.classList.contains('code128-quiet-zone')) return 320;
      return 320;
    });
    const { container: narrow } = render(
      <MantineProvider env="test">
        <div style={{ width: 320 }}>
          <Code128Mark value={PAIRING} quietZone />
        </div>
      </MantineProvider>,
    );
    await waitFor(() => {
      expect(narrow.querySelector('svg.pairing-code128')?.getAttribute('data-module-device-px')).toBe('1');
    });
    expect(within(narrow).getByText(PAIRING_ON_SCREEN_TOO_SMALL_HINT)).toBeInTheDocument();
    clientWidthSpy.mockRestore();

    vi.stubGlobal('devicePixelRatio', 2);
    const wideClientWidthSpy = vi.spyOn(HTMLDivElement.prototype, 'clientWidth', 'get').mockImplementation(function clientWidth(this: HTMLDivElement) {
      if (this.classList.contains('pairing-code128-frame')) return 360;
      if (this.classList.contains('code128-quiet-zone')) return 360;
      return 360;
    });
    const { container: wide } = render(
      <MantineProvider env="test">
        <div style={{ width: 360 }}>
          <Code128Mark value={PAIRING} quietZone />
        </div>
      </MantineProvider>,
    );
    await waitFor(() => {
      const modulePx = wide.querySelector('svg.pairing-code128')?.getAttribute('data-module-device-px');
      expect(modulePx).not.toBe('1');
    });
    expect(within(wide).queryByText(PAIRING_ON_SCREEN_TOO_SMALL_HINT)).toBeNull();
    wideClientWidthSpy.mockRestore();
  });
});

describe('printable barcode geometry and quiet zone', () => {
  it('offsets bars by quiet modules and enforces print quiet padding', () => {
    const { container } = render(
      <PrintableBarcode value="STOCK-OUT" moduleMm={0.5} heightMm={18} />,
    );
    expect(rectSequence(container)).toEqual(expectedBarRuns('STOCK-OUT'));
    const block = container.querySelector('.printable-barcode-block')!;
    expect(Number(block.getAttribute('data-quiet-mm'))).toBe(printableBarcodeQuietMm(0.5));
    expect(Number(block.getAttribute('data-quiet-mm'))).toBeGreaterThanOrEqual(6);
    expect(Number(block.getAttribute('data-quiet-mm'))).toBeGreaterThanOrEqual(10 * 0.5);
    const svg = container.querySelector('svg[data-barcode]')!;
    const firstBar = svg.querySelector('rect')!;
    expect(Number(firstBar.getAttribute('x'))).toBeGreaterThanOrEqual(PAIRING_CODE128_QUIET_MODULES);
  });

  it('matches pairingCode128BarRuns for the pairing value', () => {
    expect(pairingCode128BarRuns(PAIRING)).toEqual(expectedBarRuns(PAIRING));
  });
});

describe('PairingDialog nowrap pairing code', () => {
  it('applies pairing-spaced for nowrap styling in the dialog', async () => {
    vi.stubGlobal('fetch', vi.fn((input: RequestInfo | URL) => {
      const url = typeof input === 'string' ? input : input instanceof URL ? input.href : input.url;
      if (url.includes('/api/devices/pair')) {
        return Promise.resolve(new Response(JSON.stringify({
          code: PAIRING,
          barcodeValue: PAIRING,
          expiresAt: '2099-01-01T00:00:00Z',
          pairingId: 'pair-1',
        }), { headers: { 'Content-Type': 'application/json' } }));
      }
      return Promise.reject(new Error(url));
    }));
    render(
      <MantineProvider env="test">
        <PairingDialog opened householdName="Pantry" onClose={() => undefined} />
      </MantineProvider>,
    );
    const spaced = await screen.findByLabelText('Pairing code');
    expect(spaced).toHaveClass('pairing-spaced');
    expect(spaced.className).toMatch(/pairing-spaced/);
  });
});
