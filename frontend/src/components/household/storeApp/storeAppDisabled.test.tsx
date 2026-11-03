import { act, fireEvent, render, screen, waitFor, within } from '@testing-library/react';
import type { ReactElement } from 'react';
import { MantineProvider } from '@mantine/core';
import { MemoryRouter } from 'react-router-dom';
import { beforeEach, describe, expect, it, vi } from 'vitest';
import { writeKrogerAppSetupProgress } from '@stockbot/shared/krogerAppSetupProgress';
import { krogerAppCopy } from '@stockbot/shared/krogerAppCopy';
import type { KrogerApp } from '@stockbot/shared/types';
import { cssVariablesResolver, theme } from '../../../theme';
import { StoreAppSection } from './StoreAppSection';
import { KrogerAppSetupModal } from './KrogerAppSetupModal';

const json = (body: unknown, status = 200) => new Response(JSON.stringify(body), {
  status,
  headers: { 'Content-Type': 'application/json' },
});

const configuredApp: KrogerApp = {
  configured: true,
  clientIdLast4: '9999',
  redirectUri: 'https://app.example/cb',
  modality: 'PICKUP',
  productAccess: 'unknown',
  status: 'connected',
  canManage: true,
};

const notSetUpApp: KrogerApp = {
  configured: false,
  clientIdLast4: null,
  redirectUri: 'https://app.example/cb',
  modality: 'PICKUP',
  productAccess: 'unknown',
  status: 'not_set_up',
  canManage: true,
};

const disabled409 = () => json({ code: 'store_disabled', error: 'RAW-SERVER-DISABLED' }, 409);

const storeDisabledSentence = /Online store features are turned off/i;

function testProvider(ui: ReactElement) {
  return render(
    <MantineProvider theme={theme} cssVariablesResolver={cssVariablesResolver} env="test">
      <MemoryRouter>{ui}</MemoryRouter>
    </MantineProvider>,
  );
}

function stubSectionFetch(handlers: {
  onTest?: () => Response;
  onPut?: () => Response;
  onDelete?: () => Response;
  onAuthorize?: () => Response;
  getApp?: KrogerApp;
}) {
  const getApp = handlers.getApp ?? configuredApp;
  vi.stubGlobal('fetch', vi.fn((input: RequestInfo | URL, init?: RequestInit) => {
    const url = String(input);
    const method = init?.method ?? 'GET';
    if (url === '/api/households/hh-d/kroger-app' && method === 'GET') {
      return Promise.resolve(json(getApp));
    }
    if (url === '/api/providers' && method === 'GET') {
      return Promise.resolve(json([]));
    }
    if (url.includes('/api/providers/kroger/authorize') && handlers.onAuthorize) {
      return Promise.resolve(handlers.onAuthorize());
    }
    if (url === '/api/households/hh-d/kroger-app/test' && method === 'POST' && handlers.onTest) {
      return Promise.resolve(handlers.onTest());
    }
    if (url === '/api/households/hh-d/kroger-app' && method === 'PUT' && handlers.onPut) {
      return Promise.resolve(handlers.onPut());
    }
    if (url === '/api/households/hh-d/kroger-app' && method === 'DELETE' && handlers.onDelete) {
      return Promise.resolve(handlers.onDelete());
    }
    return Promise.reject(new Error(`${method} ${url}`));
  }));
}

function renderSection(handlers: {
  onTest?: () => Response;
  onPut?: () => Response;
  onDelete?: () => Response;
  onAuthorize?: () => Response;
  getApp?: KrogerApp;
} = {}) {
  stubSectionFetch(handlers);
  return testProvider(
    <StoreAppSection householdId="hh-d" householdName="Pantry" isAdmin linkedStoreName="Kroger" />,
  );
}

async function expectRowHidden() {
  await waitFor(() => {
    expect(screen.queryByRole('heading', { name: krogerAppCopy.rowTitle })).not.toBeInTheDocument();
  });
  expect(screen.queryByRole('button', { name: krogerAppCopy.menuMore })).not.toBeInTheDocument();
  expect(screen.queryByRole('dialog')).not.toBeInTheDocument();
  expect(document.body.textContent).not.toContain('RAW-SERVER-DISABLED');
  expect(screen.queryByText(storeDisabledSentence)).not.toBeInTheDocument();
  expect(document.querySelector('.store-app-section')).toBeNull();
}

describe('store_disabled hides the store app row', () => {
  beforeEach(() => {
    localStorage.clear();
    vi.unstubAllGlobals();
  });

  it('Test again via menu returns store_disabled without raw server text', async () => {
    renderSection({ onTest: disabled409 });
    fireEvent.click(await screen.findByRole('button', { name: krogerAppCopy.menuMore }));
    fireEvent.click(screen.getByRole('menuitem', { name: krogerAppCopy.menuTest }));
    await act(async () => { await Promise.resolve(); });
    await expectRowHidden();
  });

  it('Replace keys Test and save returns store_disabled without raw server text', async () => {
    renderSection({ onPut: disabled409 });
    fireEvent.click(await screen.findByRole('button', { name: krogerAppCopy.menuMore }));
    fireEvent.click(screen.getByRole('menuitem', { name: krogerAppCopy.menuReplace }));
    const dialog = await screen.findByRole('dialog', { name: krogerAppCopy.menuReplace });
    fireEvent.change(within(dialog).getByLabelText(/App ID/i), { target: { value: 'new-id' } });
    fireEvent.change(within(dialog).getByLabelText(/App secret/i), { target: { value: 'new-secret' } });
    fireEvent.click(within(dialog).getByRole('button', { name: krogerAppCopy.s6Test }));
    await act(async () => { await Promise.resolve(); });
    await expectRowHidden();
  });

  it('modality PUT returns store_disabled without raw server text', async () => {
    renderSection({ onPut: disabled409 });
    fireEvent.click(await screen.findByRole('button', { name: krogerAppCopy.menuMore }));
    fireEvent.click(screen.getByRole('menuitem', { name: krogerAppCopy.menuReplace }));
    const dialog = await screen.findByRole('dialog', { name: krogerAppCopy.menuReplace });
    fireEvent.click(within(dialog).getByRole('radio', { name: krogerAppCopy.modalityOptionDelivery }));
    await act(async () => { await Promise.resolve(); });
    await expectRowHidden();
  });

  it('DELETE remove returns store_disabled without raw server text', async () => {
    renderSection({ onDelete: disabled409 });
    fireEvent.click(await screen.findByRole('button', { name: krogerAppCopy.menuMore }));
    fireEvent.click(screen.getByRole('menuitem', { name: krogerAppCopy.menuRemove }));
    const dialog = await screen.findByRole('dialog', { name: krogerAppCopy.rmTitle });
    fireEvent.click(within(dialog).getByRole('button', { name: krogerAppCopy.rmConfirm }));
    await act(async () => { await Promise.resolve(); });
    await expectRowHidden();
  });

  it('setup Test and save PUT returns store_disabled without raw server text', async () => {
    writeKrogerAppSetupProgress('hh-d', {
      step: 6,
      done: [1, 2, 3, 4, 5],
      suggestedName: 'Pantry',
      returnTo: 'household',
      updatedAt: new Date().toISOString(),
    });
    const fetchMock = vi.fn((input: RequestInfo | URL, init?: RequestInit) => {
      const url = String(input);
      const method = init?.method ?? 'GET';
      if (url === '/api/households/hh-d/kroger-app' && method === 'PUT') {
        return Promise.resolve(disabled409());
      }
      if (url === '/api/households/hh-d/kroger-app' && method === 'GET') {
        return Promise.resolve(json(notSetUpApp));
      }
      if (url === '/api/providers' && method === 'GET') {
        return Promise.resolve(json([]));
      }
      return Promise.reject(new Error(`${method} ${url}`));
    });
    vi.stubGlobal('fetch', fetchMock);
    testProvider(
      <StoreAppSection householdId="hh-d" householdName="Pantry" isAdmin linkedStoreName="Kroger" />,
    );
    fireEvent.click(await screen.findByRole('button', { name: krogerAppCopy.rowSetUp }));
    fireEvent.change(await screen.findByLabelText(/App ID/i), { target: { value: 'new-id' } });
    fireEvent.change(screen.getByLabelText(/App secret/i), { target: { value: 'new-secret' } });
    fireEvent.click(screen.getByRole('button', { name: krogerAppCopy.s6Test }));
    await waitFor(() => {
      expect(fetchMock).toHaveBeenCalledWith(
        expect.stringContaining('/api/households/hh-d/kroger-app'),
        expect.objectContaining({ method: 'PUT' }),
      );
    });
    await expectRowHidden();
  });

  it('step 7 Connect returns store_disabled without raw server text', async () => {
    const onStoreDisabled = vi.fn();
    vi.stubGlobal('fetch', vi.fn((input: RequestInfo | URL) => {
      const url = String(input);
      if (url.includes('/api/providers/kroger/authorize')) {
        return Promise.resolve(disabled409());
      }
      return Promise.reject(new Error(url));
    }));
    testProvider(
      <KrogerAppSetupModal
        opened
        onClose={() => undefined}
        householdId="hh-d"
        householdName="Pantry"
        linkedStoreName="Kroger"
        app={{ ...configuredApp, status: 'ready_to_connect' }}
        initialStep={7}
        onSaved={() => undefined}
        onStoreDisabled={onStoreDisabled}
      />,
    );
    fireEvent.click(screen.getByRole('button', { name: /Connect your Kroger account/i }));
    await waitFor(() => expect(onStoreDisabled).toHaveBeenCalled());
    expect(screen.queryByText(storeDisabledSentence)).not.toBeInTheDocument();
  });

  it('row Connect returns store_disabled without raw server text', async () => {
    renderSection({
      onAuthorize: disabled409,
      getApp: { ...configuredApp, status: 'ready_to_connect' },
    });
    fireEvent.click(await screen.findByRole('button', { name: krogerAppCopy.rowConnect }));
    await act(async () => { await Promise.resolve(); });
    await expectRowHidden();
  });

  it('row Reconnect returns store_disabled without raw server text', async () => {
    renderSection({
      onAuthorize: disabled409,
      getApp: { ...configuredApp, status: 'needs_reconnect' },
    });
    fireEvent.click(await screen.findByRole('button', { name: /Connect your Kroger account again/i }));
    await act(async () => { await Promise.resolve(); });
    await expectRowHidden();
  });
});

describe('store_disabled in setup modal', () => {
  beforeEach(() => {
    localStorage.clear();
    vi.unstubAllGlobals();
  });

  it('modality-only PUT from step 6 invokes onStoreDisabled', async () => {
    const onStoreDisabled = vi.fn();
    vi.stubGlobal('fetch', vi.fn((input: RequestInfo | URL, init?: RequestInit) => {
      const url = String(input);
      const method = init?.method ?? 'GET';
      if (url === '/api/households/hh-d/kroger-app' && method === 'PUT') {
        return Promise.resolve(disabled409());
      }
      return Promise.reject(new Error(`${method} ${url}`));
    }));
    testProvider(
      <KrogerAppSetupModal
        opened
        onClose={() => undefined}
        householdId="hh-d"
        householdName="Pantry"
        linkedStoreName="Kroger"
        app={configuredApp}
        replaceMode
        initialStep={6}
        onSaved={() => undefined}
        onStoreDisabled={onStoreDisabled}
      />,
    );
    fireEvent.click(screen.getByRole('radio', { name: krogerAppCopy.modalityOptionDelivery }));
    await waitFor(() => expect(onStoreDisabled).toHaveBeenCalled());
    expect(screen.queryByText(storeDisabledSentence)).not.toBeInTheDocument();
  });

  it('modality-only PUT not_configured shows removed copy with Start again', async () => {
    vi.stubGlobal('fetch', vi.fn((input: RequestInfo | URL, init?: RequestInit) => {
      const url = String(input);
      const method = init?.method ?? 'GET';
      if (url === '/api/households/hh-d/kroger-app' && method === 'PUT') {
        return Promise.resolve(json({
          code: 'kroger_not_connected',
          error: 'not configured',
          checkReason: 'not_configured',
        }, 409));
      }
      return Promise.reject(new Error(`${method} ${url}`));
    }));
    testProvider(
      <KrogerAppSetupModal
        opened
        onClose={() => undefined}
        householdId="hh-d"
        householdName="Pantry"
        linkedStoreName="Kroger"
        app={configuredApp}
        replaceMode
        initialStep={6}
        onSaved={() => undefined}
      />,
    );
    fireEvent.click(screen.getByRole('radio', { name: krogerAppCopy.modalityOptionDelivery }));
    await screen.findByText(krogerAppCopy.errRemoved);
    expect(screen.getByRole('button', { name: krogerAppCopy.startAgain })).toBeInTheDocument();
  });
});
