import { useEffect, useState } from 'react';
import { Stack, Table, Text, Title } from '@mantine/core';
import { applyHttpResponse, AUTH_NOT_CONFIGURED_SENTENCE, inlineFailure, notifyApiSuccess, raiseOfflineBanner, ServerNoticeError, stockbotCode, UnreachableError, unreachableFrom, unreadableResponse } from '../../api/contract';

interface LatencySnapshot {
  count: number;
  maxMs: number;
  p50Ms: number;
  p95Ms: number;
}

interface RouteTiming {
  route: string;
  calls: number;
  errors?: number;
  latency: LatencySnapshot;
}

interface AssetSnapshot {
  maxResources: number;
  maxTransferBytes: number;
  maxEncodedBytes: number;
}

interface PageLoadLatest {
  page?: string;
  durationMs: number;
  ttfbMs: number;
  domContentLoadedMs: number;
  documentMs: number;
  afterDomMs: number;
  jsResources?: number;
  jsTransferBytes?: number;
  jsEncodedBytes?: number;
  cssResources?: number;
  cssTransferBytes?: number;
  cssEncodedBytes?: number;
}

interface PageLoadSnapshot {
  samples: number;
  duration: LatencySnapshot;
  ttfb: LatencySnapshot;
  domContentLoaded: LatencySnapshot;
  latest?: PageLoadLatest;
  js: AssetSnapshot;
  css: AssetSnapshot;
  firstPaintApi: RouteTiming[];
  slowestClientApi: RouteTiming[];
  slowestHttp: RouteTiming[];
  dominant: string;
  note: string;
}

interface TelemetrySnapshot {
  pageLoad: PageLoadSnapshot;
}

const formatMs = (ms: number) => `${Math.round(ms)} ms`;

const formatBytes = (bytes: number) => {
  if (bytes < 1024) return `${bytes} B`;
  if (bytes < 1024 * 1024) return `${Math.round(bytes / 1024)} KB`;
  return `${(bytes / (1024 * 1024)).toFixed(1)} MB`;
};

const assetLine = (label: string, latestCount: number | undefined, latestTransfer: number | undefined, latestEncoded: number | undefined, max: AssetSnapshot) => {
  const count = latestCount ?? max.maxResources;
  const transfer = latestTransfer ?? max.maxTransferBytes;
  const encoded = latestEncoded ?? max.maxEncodedBytes;
  if (count === 0 && transfer === 0 && encoded === 0) {
    return `${label}: no files reported`;
  }
  const encodedNote = encoded > transfer ? `, ${formatBytes(encoded)} encoded` : '';
  const files = count === 1 ? '1 file' : `${count} files`;
  return `${label}: ${files}, ${formatBytes(transfer)} transferred${encodedNote}`;
};

const RouteTable = ({ rows, empty }: { rows: RouteTiming[]; empty: string }) => {
  if (rows.length === 0) {
    return <Text c="dimmed" size="sm">{empty}</Text>;
  }
  return (
    <Table horizontalSpacing="xs" verticalSpacing={4}>
      <Table.Thead>
        <Table.Tr>
          <Table.Th>Route</Table.Th>
          <Table.Th>Calls</Table.Th>
          <Table.Th>p50</Table.Th>
          <Table.Th>p95</Table.Th>
          <Table.Th>Max</Table.Th>
        </Table.Tr>
      </Table.Thead>
      <Table.Tbody>
        {rows.map((row) => (
          <Table.Tr key={row.route}>
            <Table.Td>{row.route}</Table.Td>
            <Table.Td>{row.calls}{row.errors ? ` (${row.errors} failed)` : ''}</Table.Td>
            <Table.Td>{formatMs(row.latency.p50Ms)}</Table.Td>
            <Table.Td>{formatMs(row.latency.p95Ms)}</Table.Td>
            <Table.Td>{formatMs(row.latency.maxMs)}</Table.Td>
          </Table.Tr>
        ))}
      </Table.Tbody>
    </Table>
  );
};

export const DiagnosticsPage = () => {
  const [snapshot, setSnapshot] = useState<TelemetrySnapshot | null>(null);
  const [error, setError] = useState('');
  const [pending, setPending] = useState(true);

  useEffect(() => {
    let cancelled = false;
    void (async () => {
      try {
        let res: Response;
        try {
          res = await fetch('/api/telemetry');
        } catch (error) {
          const unreachable = unreachableFrom(error);
          if (unreachable !== null) {
            raiseOfflineBanner();
            throw unreachable;
          }
          throw error;
        }
        if (!res.ok) {
          let code = '';
          try {
            code = stockbotCode(await res.json());
          } catch {
            code = '';
          }
          const disposition = applyHttpResponse(res.status, code);
          if (disposition === 'offline') throw new UnreachableError();
          if (disposition === 'auth') throw new ServerNoticeError('auth_not_configured', AUTH_NOT_CONFIGURED_SENTENCE);
          throw new Error('Unable to load diagnostics.');
        }
        let text: string;
        try {
          text = await res.text();
        } catch (error) {
          throw unreadableResponse(error);
        }
        let body: TelemetrySnapshot;
        try {
          body = JSON.parse(text) as TelemetrySnapshot;
        } catch (error) {
          throw unreadableResponse(error);
        }
        if (cancelled) return;
        notifyApiSuccess();
        setError('');
        setPending(false);
        setSnapshot(body);
      } catch (error) {
        if (cancelled) return;
        setPending(false);
        setError(inlineFailure(error, 'Unable to load diagnostics.') ?? '');
      }
    })();
    return () => {
      cancelled = true;
    };
  }, []);

  const pageLoad = snapshot?.pageLoad;
  const latest = pageLoad?.latest;

  return (
    <Stack gap="sm">
      <Title order={1} size="h3">Diagnostics</Title>
      <Text c="dimmed" size="sm">
        Timings from this Stockbot. Barcodes and product names are left out.
      </Text>
      {error !== '' && <Text c="red">{error}</Text>}
      {snapshot === null && error === '' && pending && <Text c="dimmed">Loading diagnostics.</Text>}
      {pageLoad !== undefined && (
        <Stack gap="xs">
          <Text>{pageLoad.note}</Text>
          {latest !== undefined && (
            <Stack gap={2}>
              <Text size="sm">Page: {latest.page || '/'}</Text>
              <Text size="sm">Time to first byte: {formatMs(latest.ttfbMs)}</Text>
              <Text size="sm">DOM ready: {formatMs(latest.domContentLoadedMs)}</Text>
              <Text size="sm">Document after first byte: {formatMs(latest.documentMs)}</Text>
              <Text size="sm">
                Full load: {latest.durationMs > 0 ? formatMs(latest.durationMs) : 'not recorded'}
              </Text>
              <Text size="sm">After DOM ready: {latest.durationMs > 0 ? formatMs(latest.afterDomMs) : 'not recorded'}</Text>
              <Text size="sm">{assetLine('JS', latest.jsResources, latest.jsTransferBytes, latest.jsEncodedBytes, pageLoad.js)}</Text>
              <Text size="sm">{assetLine('CSS', latest.cssResources, latest.cssTransferBytes, latest.cssEncodedBytes, pageLoad.css)}</Text>
            </Stack>
          )}
          <Text size="sm" c="dimmed">
            Recent page loads: {pageLoad.samples}. p50 / p95 are bucket upper bounds
            (first byte {formatMs(pageLoad.ttfb.p50Ms)} / {formatMs(pageLoad.ttfb.p95Ms)},
            DOM ready {formatMs(pageLoad.domContentLoaded.p50Ms)} / {formatMs(pageLoad.domContentLoaded.p95Ms)},
            full load {formatMs(pageLoad.duration.p50Ms)} / {formatMs(pageLoad.duration.p95Ms)}).
          </Text>
          <Title order={2} size="h4">First API calls</Title>
          <RouteTable rows={pageLoad.firstPaintApi} empty="No first-paint API calls yet." />
          <Title order={2} size="h4">Slowest browser API calls</Title>
          <RouteTable rows={pageLoad.slowestClientApi} empty="No browser API timings yet." />
          <Title order={2} size="h4">Slowest server routes</Title>
          <Text c="dimmed" size="sm">GET / includes the page and the UI files.</Text>
          <RouteTable rows={pageLoad.slowestHttp} empty="No server timings yet." />
        </Stack>
      )}
    </Stack>
  );
};
