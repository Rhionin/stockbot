import { render } from '@testing-library/react';
import { MantineProvider, Text } from '@mantine/core';
import { beforeAll, describe, expect, it } from 'vitest';
import { formatPairingCode } from '@stockbot/shared/contract';
import {
  PAIRING_CODE128_MIN_MODULE_PX,
  PAIRING_CODE128_QUIET_MODULES,
  computePairingCode128ModulePx,
} from './pairingScreenCode128';
import { Code128Mark } from './symbols';
import householdCss from './household.css?raw';

const PAIRING = formatPairingCode('23456789');

beforeAll(() => {
  const style = document.createElement('style');
  style.textContent = householdCss;
  document.head.appendChild(style);
});

describe('on-screen pairing Code 128 (PR #466 follow-up)', () => {
  it('uses integer module width >= 2px and quiet zone >= 10 modules', () => {
    const { container } = render(
      <MantineProvider env="test">
        <div style={{ width: 390 }}>
          <Code128Mark value={PAIRING} quietZone />
        </div>
      </MantineProvider>,
    );
    const svg = container.querySelector('svg.pairing-code128');
    expect(svg).not.toBeNull();
    const modulePx = Number(svg!.getAttribute('data-module-px'));
    expect(Number.isInteger(modulePx)).toBe(true);
    expect(modulePx).toBeGreaterThanOrEqual(PAIRING_CODE128_MIN_MODULE_PX);
    expect(Number(svg!.getAttribute('data-quiet-modules'))).toBeGreaterThanOrEqual(PAIRING_CODE128_QUIET_MODULES);
    expect([...svg!.querySelectorAll('rect')].every((rect) => rect.getAttribute('fill') === '#000000')).toBe(true);
    expect(svg!.getAttribute('shape-rendering')).toBe('crispEdges');
  });

  it('computePairingCode128ModulePx never returns below minimum', () => {
    expect(computePairingCode128ModulePx(100, 200)).toBe(PAIRING_CODE128_MIN_MODULE_PX);
    const wide = computePairingCode128ModulePx(800, 200);
    expect(wide).toBeGreaterThanOrEqual(PAIRING_CODE128_MIN_MODULE_PX);
    expect(Number.isInteger(wide)).toBe(true);
  });
});

describe('pairing code text layout', () => {
  it('keeps letter-spaced pairing code on one line', () => {
    const { container } = render(
      <MantineProvider env="test">
        <Text className="pairing-spaced" aria-label="Pairing code">
          {PAIRING.split('').join(' ')}
        </Text>
      </MantineProvider>,
    );
    const code = container.querySelector('.pairing-spaced')!;
    expect(window.getComputedStyle(code).whiteSpace).toBe('nowrap');
  });
});
