import { render, waitFor } from '@testing-library/react';
import { MantineProvider, Text } from '@mantine/core';
import { afterEach, describe, expect, it, vi } from 'vitest';
import { formatPairingCode } from '@stockbot/shared/contract';
import {
  PAIRING_CODE128_MIN_MODULE_DEVICE_PX,
  PAIRING_CODE128_QUIET_MODULES,
  computePairingCode128Layout,
  pairingCode128ModuleCount,
} from './pairingScreenCode128';
import { Code128Mark } from './symbols';

const PAIRING = formatPairingCode('23456789');
const BAR_MODULES = pairingCode128ModuleCount(PAIRING);

describe('on-screen pairing Code 128 (PR #466 follow-up)', () => {
  afterEach(() => {
    vi.unstubAllGlobals();
  });

  it('uses device-pixel modules, quiet zone >= 10, and fits the container width', async () => {
    vi.stubGlobal('devicePixelRatio', 3);
    const { container } = render(
      <MantineProvider env="test">
        <div style={{ width: 366 }}>
          <Code128Mark value={PAIRING} quietZone />
        </div>
      </MantineProvider>,
    );
    await waitFor(() => {
      const svg = container.querySelector('svg.pairing-code128');
      expect(svg).not.toBeNull();
      expect(Number(svg!.getAttribute('width'))).toBeLessThanOrEqual(366);
    });
    const svg = container.querySelector('svg.pairing-code128')!;
    const moduleDevicePx = Number(svg!.getAttribute('data-module-device-px'));
    expect(moduleDevicePx).toBeGreaterThanOrEqual(PAIRING_CODE128_MIN_MODULE_DEVICE_PX);
    expect(Number.isInteger(moduleDevicePx)).toBe(true);
    expect(Number(svg!.getAttribute('data-quiet-modules'))).toBeGreaterThanOrEqual(PAIRING_CODE128_QUIET_MODULES);
    expect([...svg!.querySelectorAll('rect')].every((rect) => rect.getAttribute('fill') === '#000000')).toBe(true);
    expect(svg.getAttribute('shape-rendering')).toBe('crispEdges');
  });

  it('computePairingCode128Layout matches narrow high-DPR and desktop cases', () => {
    const narrow = computePairingCode128Layout(366, BAR_MODULES, 3);
    expect(narrow.moduleDevicePx).toBe(4);
    expect(narrow.moduleCssPx).toBeCloseTo(4 / 3, 5);
    expect(narrow.svgWidthCssPx).toBeCloseTo(293.333, 1);

    const desktop = computePairingCode128Layout(517, BAR_MODULES, 1);
    expect(desktop.moduleDevicePx).toBe(2);
    expect(desktop.moduleCssPx).toBe(2);
    expect(desktop.svgWidthCssPx).toBe(440);

    const tiny = computePairingCode128Layout(50, BAR_MODULES, 1);
    expect(tiny.moduleDevicePx).toBeGreaterThanOrEqual(1);
    expect(computePairingCode128Layout(366, BAR_MODULES, 3).moduleDevicePx).toBeGreaterThanOrEqual(
      PAIRING_CODE128_MIN_MODULE_DEVICE_PX,
    );
  });
});

describe('pairing code text layout', () => {
  it('applies the pairing-spaced class for nowrap styling', () => {
    const { container } = render(
      <MantineProvider env="test">
        <Text className="pairing-spaced" aria-label="Pairing code">
          {PAIRING.split('').join(' ')}
        </Text>
      </MantineProvider>,
    );
    expect(container.querySelector('.pairing-spaced')).not.toBeNull();
  });
});
