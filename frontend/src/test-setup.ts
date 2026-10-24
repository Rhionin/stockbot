// Vitest setup file (configured via vite.config.ts test.setupFiles).
// Extends expect() with jest-dom matchers for component tests.
import '@testing-library/jest-dom/vitest';
import { afterEach } from 'vitest';
import { cleanup } from '@testing-library/react';
import { clearServerNotice } from '@stockbot/shared/contract';
import { resetStage1Client } from './api/client';

// vite.config.ts does not set test.globals, so React Testing Library's
// automatic afterEach cleanup (which relies on a global afterEach) never
// registers. Wire it up explicitly so each test starts with a clean DOM.
afterEach(() => {
  resetStage1Client();
  clearServerNotice();
  cleanup();
});

// jsdom does not implement matchMedia, but Mantine's color scheme detection
// calls it on every mount. Stub it so components wrapped in MantineProvider
// can be rendered in tests.
if (typeof window.matchMedia !== 'function') {
  window.matchMedia = (query: string) => ({
    matches: false,
    media: query,
    onchange: null,
    addListener: () => {},
    removeListener: () => {},
    addEventListener: () => {},
    removeEventListener: () => {},
    dispatchEvent: () => false,
  }) as unknown as MediaQueryList;
}

// jsdom does not implement ResizeObserver, but Mantine's SegmentedControl
// uses it (via FloatingIndicator) to position the selected-segment
// highlight. Stub it so components wrapped in MantineProvider can render.
// Mantine combobox scrolls the active option into view. jsdom does not
// implement Element.scrollIntoView, and the call is scheduled on a timer.
if (typeof Element.prototype.scrollIntoView !== 'function') {
  Element.prototype.scrollIntoView = () => {};
}

if (typeof window.ResizeObserver !== 'function') {
  window.ResizeObserver = class {
    observe() {}
    unobserve() {}
    disconnect() {}
  };
}
