import { render, screen } from '@testing-library/react';
import { MantineProvider } from '@mantine/core';
import { afterEach, describe, expect, it, vi } from 'vitest';
import { AUTH_NOT_CONFIGURED_SENTENCE, notifyApiSuccess, offlineBannerActive, OFFLINE_SENTENCE, raiseOfflineBanner } from '@stockbot/shared/contract';
import { ServerNoticeBanner } from '../shell/ServerNoticeBanner';
import { DiagnosticsPage } from './DiagnosticsPage';

const jsonResponse = (body: unknown, status = 200) =>
  new Response(JSON.stringify(body), { status, headers: { 'Content-Type': 'application/json' } });

const renderPage = () =>
  render(
    <MantineProvider>
      <ServerNoticeBanner />
      <DiagnosticsPage />
    </MantineProvider>,
  );

const latency = (maxMs: number, p50Ms: number, p95Ms: number) => ({
  count: 1,
  maxMs,
  p50Ms,
  p95Ms,
});

describe('DiagnosticsPage', () => {
  afterEach(() => {
    notifyApiSuccess();
    vi.unstubAllGlobals();
  });

  it('renders the page-load gaps, assets, and slow routes from the snapshot', async () => {
    const fetchMock = vi.fn().mockResolvedValue(jsonResponse({
      pageLoad: {
        samples: 1,
        duration: latency(1400, 2500, 2500),
        ttfb: latency(202, 250, 250),
        domContentLoaded: latency(1297, 2500, 2500),
        latest: {
          page: '/',
          durationMs: 1400,
          ttfbMs: 202,
          domContentLoadedMs: 1297,
          documentMs: 1095,
          afterDomMs: 103,
          jsResources: 2,
          jsTransferBytes: 840 * 1024,
          jsEncodedBytes: 1100 * 1024,
          cssResources: 1,
          cssTransferBytes: 40 * 1024,
        },
        js: { maxResources: 2, maxTransferBytes: 840 * 1024, maxEncodedBytes: 1100 * 1024 },
        css: { maxResources: 1, maxTransferBytes: 40 * 1024, maxEncodedBytes: 40 * 1024 },
        firstPaintApi: [
          { route: '/api/scans', calls: 2, latency: latency(40, 50, 50) },
          { route: '/api/inventory', calls: 1, latency: latency(34, 50, 50) },
        ],
        slowestClientApi: [
          { route: '/api/scans', calls: 2, latency: latency(40, 50, 50) },
        ],
        slowestHttp: [
          { route: 'GET /', calls: 8, latency: latency(565, 500, 1000) },
        ],
        dominant: 'document',
        note: 'Latest page load: first byte 202ms, DOM ready 1297ms, full load 1400ms. Most of the wait is the document after the first byte (1095ms).',
      },
    }));
    vi.stubGlobal('fetch', fetchMock);

    renderPage();

    expect(await screen.findByText(/Most of the wait is the document after the first byte/)).toBeInTheDocument();
    expect(screen.getByText('Time to first byte: 202 ms')).toBeInTheDocument();
    expect(screen.getByText('DOM ready: 1297 ms')).toBeInTheDocument();
    expect(screen.getByText('Document after first byte: 1095 ms')).toBeInTheDocument();
    expect(screen.getByText('Full load: 1400 ms')).toBeInTheDocument();
    expect(screen.getByText('JS: 2 files, 840 KB transferred, 1.1 MB encoded')).toBeInTheDocument();
    expect(screen.getByText('CSS: 1 file, 40 KB transferred')).toBeInTheDocument();
    expect(screen.getByText('/api/inventory')).toBeInTheDocument();
    expect(screen.getByText('GET /')).toBeInTheDocument();
    expect(fetchMock).toHaveBeenCalledWith('/api/telemetry');
    expect(JSON.stringify(fetchMock.mock.calls)).not.toContain('barcode');
  });

  it('says when full load was not recorded', async () => {
    vi.stubGlobal('fetch', vi.fn().mockResolvedValue(jsonResponse({
      pageLoad: {
        samples: 1,
        duration: latency(0, 1, 1),
        ttfb: latency(202, 250, 250),
        domContentLoaded: latency(1297, 2500, 2500),
        latest: {
          page: '/',
          durationMs: 0,
          ttfbMs: 202,
          domContentLoadedMs: 1297,
          documentMs: 1095,
          afterDomMs: 0,
        },
        js: { maxResources: 0, maxTransferBytes: 0, maxEncodedBytes: 0 },
        css: { maxResources: 0, maxTransferBytes: 0, maxEncodedBytes: 0 },
        firstPaintApi: [],
        slowestClientApi: [],
        slowestHttp: [],
        dominant: 'document',
        note: 'Latest page load: first byte 202ms, DOM ready 1297ms, full load was not recorded.',
      },
    })));

    renderPage();

    expect(await screen.findByText('Full load: not recorded')).toBeInTheDocument();
    expect(screen.getByText('No first-paint API calls yet.')).toBeInTheDocument();
  });

  it('shows the sign-in banner when auth is not configured', async () => {
    vi.stubGlobal('fetch', vi.fn().mockResolvedValue(jsonResponse({ error: 'off', code: 'auth_not_configured' }, 503)));
    renderPage();
    expect(await screen.findByTestId('server-notice-banner')).toHaveTextContent(AUTH_NOT_CONFIGURED_SENTENCE);
    expect(screen.queryByText(OFFLINE_SENTENCE)).not.toBeInTheDocument();
    expect(screen.queryByText('Unable to load diagnostics.')).not.toBeInTheDocument();
  });

  it('shows the offline banner for a gateway diagnostics page', async () => {
    vi.stubGlobal('fetch', vi.fn().mockResolvedValue(new Response('<html>gateway</html>', { status: 503 })));
    renderPage();
    expect(await screen.findByTestId('server-notice-banner')).toHaveTextContent(OFFLINE_SENTENCE);
    expect(screen.queryByText('Unable to load diagnostics.')).not.toBeInTheDocument();
  });

  it('shows an error when the snapshot cannot be loaded', async () => {
    vi.stubGlobal('fetch', vi.fn().mockResolvedValue(jsonResponse({ error: 'nope', code: 'internal' }, 500)));
    raiseOfflineBanner();
    renderPage();
    expect(await screen.findByText('Unable to load diagnostics.')).toBeInTheDocument();
    expect(screen.queryByTestId('server-notice-banner')).not.toBeInTheDocument();
    expect(screen.queryByText(OFFLINE_SENTENCE)).not.toBeInTheDocument();
  });

  it('shows the banner when the diagnostics body cannot be read', async () => {
    vi.stubGlobal('fetch', vi.fn(async () => ({
      ok: true,
      status: 200,
      text: () => Promise.reject(new TypeError('network error')),
    }) as Response));
    renderPage();
    expect(await screen.findByTestId('server-notice-banner')).toHaveTextContent(OFFLINE_SENTENCE);
    expect(screen.queryByText('Unable to load diagnostics.')).not.toBeInTheDocument();
    expect(screen.queryByText('network error')).not.toBeInTheDocument();
    expect(screen.getAllByText(OFFLINE_SENTENCE)).toHaveLength(1);
  });

  it('shows the banner for an HTML body and clears it after a later success', async () => {
    let html = true;
    vi.stubGlobal('fetch', vi.fn(() => Promise.resolve(html
      ? new Response('<html>', { status: 200, headers: { 'Content-Type': 'text/html' } })
      : jsonResponse({
        pageLoad: {
          samples: 0,
          duration: latency(0, 1, 1),
          ttfb: latency(0, 1, 1),
          domContentLoaded: latency(0, 1, 1),
          js: { maxResources: 0, maxTransferBytes: 0, maxEncodedBytes: 0 },
          css: { maxResources: 0, maxTransferBytes: 0, maxEncodedBytes: 0 },
          firstPaintApi: [],
          slowestClientApi: [],
          slowestHttp: [],
          dominant: '',
          note: 'Diagnostics are back.',
        },
      }))));
    const view = renderPage();
    expect(await screen.findByTestId('server-notice-banner')).toHaveTextContent(OFFLINE_SENTENCE);
    expect(screen.queryByText(/SyntaxError/)).not.toBeInTheDocument();
    expect(screen.queryByText('Unable to load diagnostics.')).not.toBeInTheDocument();
    expect(offlineBannerActive()).toBe(true);
    view.unmount();

    html = false;
    renderPage();
    expect(await screen.findByText('Diagnostics are back.')).toBeInTheDocument();
    expect(screen.queryByTestId('server-notice-banner')).not.toBeInTheDocument();
  });
});
