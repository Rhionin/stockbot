import { act, fireEvent, render, screen, waitFor, within } from '@testing-library/react';
import { MantineProvider } from '@mantine/core';
import { MemoryRouter } from 'react-router-dom';
import { beforeEach, describe, expect, it, vi } from 'vitest';
import { krogerAppCopy } from '@stockbot/shared/krogerAppCopy';
import type { KrogerApp } from '@stockbot/shared/types';
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

const disabled409 = () => json({ code: 'store_disabled', error: 'RAW-SERVER-DISABLED' }, 409);

function stubSectionFetch(handlers: {
  onTest?: () => Response;
  onPut?: () => Response;
  onDelete?: () => Response;
}) {
  vi.stubGlobal('fetch', vi.fn((input: RequestInfo | URL, init?: RequestInit) => {
    const url = String(input);
    const method = init?.method ?? 'GET';
    if (url === '/api/households/hh-d/kroger-app' && method === 'GET') {
      return Promise.resolve(json(configuredApp));
    }
    if (url === '/api/providers' && method === 'GET') {
      return Promise.resolve(json([]));
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

function renderSection() {
  return render(
    <MantineProvider env="test">
      <MemoryRouter>
        <StoreAppSection householdId="hh-d" householdName="Pantry" isAdmin linkedStoreName="Kroger" />
      </MemoryRouter>
    </MantineProvider>,
  );
}

async function expectRowHidden() {
  await waitFor(() => {
    expect(screen.queryByRole('heading', { name: krogerAppCopy.rowTitle })).not.toBeInTheDocument();
  });
  expect(screen.queryByRole('button', { name: krogerAppCopy.menuMore })).not.toBeInTheDocument();
  expect(screen.queryByRole('dialog')).not.toBeInTheDocument();
  expect(document.body.textContent).not.toContain('RAW-SERVER-DISABLED');
  expect(screen.queryByText(/Online store features are turned off/i)).not.toBeInTheDocument();
  expect(document.querySelector('.store-app-section')).toBeNull();
}

describe('store_disabled hides the store app row', () => {
  beforeEach(() => {
    localStorage.clear();
    vi.unstubAllGlobals();
  });

  it('Test again via menu returns store_disabled without raw server text', async () => {
    stubSectionFetch({ onTest: disabled409 });
    renderSection();
    fireEvent.click(await screen.findByRole('button', { name: krogerAppCopy.menuMore }));
    fireEvent.click(screen.getByRole('menuitem', { name: krogerAppCopy.menuTest }));
    await act(async () => { await Promise.resolve(); });
    await expectRowHidden();
  });

  it('Replace keys Test and save returns store_disabled without raw server text', async () => {
    stubSectionFetch({ onPut: disabled409 });
    renderSection();
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
    stubSectionFetch({ onPut: disabled409 });
    renderSection();
    fireEvent.click(await screen.findByRole('button', { name: krogerAppCopy.menuMore }));
    fireEvent.click(screen.getByRole('menuitem', { name: krogerAppCopy.menuReplace }));
    const dialog = await screen.findByRole('dialog', { name: krogerAppCopy.menuReplace });
    fireEvent.click(within(dialog).getByRole('radio', { name: krogerAppCopy.modalityOptionDelivery }));
    await act(async () => { await Promise.resolve(); });
    await expectRowHidden();
  });

  it('DELETE remove returns store_disabled without raw server text', async () => {
    stubSectionFetch({ onDelete: disabled409 });
    renderSection();
    fireEvent.click(await screen.findByRole('button', { name: krogerAppCopy.menuMore }));
    fireEvent.click(screen.getByRole('menuitem', { name: krogerAppCopy.menuRemove }));
    const dialog = await screen.findByRole('dialog', { name: krogerAppCopy.rmTitle });
    fireEvent.click(within(dialog).getByRole('button', { name: krogerAppCopy.rmConfirm }));
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
    render(
      <MantineProvider env="test">
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
        />
      </MantineProvider>,
    );
    fireEvent.click(screen.getByRole('radio', { name: krogerAppCopy.modalityOptionDelivery }));
    await waitFor(() => expect(onStoreDisabled).toHaveBeenCalled());
  });
});
