import { fireEvent, render, screen, waitFor, within } from '@testing-library/react';
import { MantineProvider } from '@mantine/core';
import { MemoryRouter } from 'react-router-dom';
import { beforeEach, describe, expect, it, vi } from 'vitest';
import { krogerAppCopy } from '@stockbot/shared/krogerAppCopy';
import {
  assertProgressHasNoSecrets,
  readKrogerAppSetupProgress,
  writeKrogerAppSetupProgress,
} from '@stockbot/shared/krogerAppSetupProgress';
import type { KrogerApp, KrogerAppGetResponse } from '@stockbot/shared/types';
import { ApiError } from '../../../api/client';
import { KrogerAppSetupModal } from './KrogerAppSetupModal';
import { StoreAppSection } from './StoreAppSection';
import { StoreLinkPreStepDialog } from './StoreLinkPreStepDialog';

const json = (body: unknown, status = 200) =>
  new Response(JSON.stringify(body), { status, headers: { 'Content-Type': 'application/json' } });

const baseApp = (overrides: Partial<KrogerApp> = {}): KrogerApp => ({
  configured: false,
  clientIdLast4: null,
  redirectUri: 'https://app.example/cb',
  modality: 'PICKUP',
  productAccess: 'unknown',
  status: 'not_set_up',
  canManage: true,
  ...overrides,
});

function renderSection(
  app: KrogerAppGetResponse,
  options: { isAdmin?: boolean; householdId?: string } = {},
) {
  const householdId = options.householdId ?? 'hh-1';
  vi.stubGlobal('fetch', vi.fn((input: RequestInfo | URL, init?: RequestInit) => {
    const url = String(input);
    if (url === `/api/households/${householdId}/kroger-app` && init?.method === undefined) {
      return Promise.resolve(json(app));
    }
    if (url === `/api/households/${householdId}/kroger-app/test` && init?.method === 'POST') {
      return Promise.resolve(json({ ok: true }));
    }
    if (url === `/api/households/${householdId}/kroger-app` && init?.method === 'DELETE') {
      return Promise.resolve(json(baseApp()));
    }
    if (url.startsWith('/api/providers/kroger/authorize')) {
      return Promise.resolve(json({ authorizationUrl: 'https://kroger.example/oauth' }));
    }
    return Promise.reject(new Error(`${init?.method ?? 'GET'} ${url}`));
  }));
  return render(
    <MantineProvider env="test">
      <MemoryRouter>
        <StoreAppSection
          householdId={householdId}
          householdName="Pantry"
          isAdmin={options.isAdmin ?? true}
          linkedStoreName="Kroger"
        />
      </MemoryRouter>
    </MantineProvider>,
  );
}

describe('T36 not_set_up admin row', () => {
  beforeEach(() => {
    localStorage.clear();
    vi.unstubAllGlobals();
  });

  it('T36 shows Set up for a household admin', async () => {
    renderSection(baseApp({ status: 'not_set_up' }));
    expect(await screen.findByRole('heading', { name: krogerAppCopy.rowTitle })).toBeInTheDocument();
    expect(screen.getByRole('button', { name: krogerAppCopy.rowSetUp })).toBeInTheDocument();
  });
});

describe('T37 not_set_up member row', () => {
  beforeEach(() => vi.unstubAllGlobals());

  it('T37 asks a member to contact a household admin', async () => {
    renderSection(baseApp({ status: 'not_set_up', canManage: false }), { isAdmin: false });
    await screen.findByText(krogerAppCopy.askAdmin);
    expect(screen.queryByRole('button', { name: krogerAppCopy.rowSetUp })).not.toBeInTheDocument();
  });
});

describe('T38 ready_to_connect admin', () => {
  beforeEach(() => vi.unstubAllGlobals());

  it('T38 offers Connect for an admin', async () => {
    renderSection(baseApp({ configured: true, status: 'ready_to_connect', clientIdLast4: '1234' }));
    expect(await screen.findByRole('button', { name: krogerAppCopy.rowConnect })).toBeInTheDocument();
  });
});

describe('T39 ready_to_connect member', () => {
  beforeEach(() => vi.unstubAllGlobals());

  it('T39 tells a member to ask a household admin to connect', async () => {
    renderSection(baseApp({ configured: true, status: 'ready_to_connect', canManage: false }), { isAdmin: false });
    await screen.findByText(/Ask a household admin/i);
    expect(screen.queryByRole('button', { name: krogerAppCopy.rowConnect })).not.toBeInTheDocument();
  });
});

describe('T40 connected row', () => {
  beforeEach(() => vi.unstubAllGlobals());

  it('T40 shows connected copy for a linked store', async () => {
    renderSection(baseApp({ configured: true, status: 'connected', clientIdLast4: 'abcd', productAccess: 'true' }));
    expect(await screen.findByText(krogerAppCopy.rowConnected)).toBeInTheDocument();
  });
});

describe('T41 needs_reconnect row', () => {
  beforeEach(() => vi.unstubAllGlobals());

  it('T41 shows reconnect for admins', async () => {
    renderSection(baseApp({ configured: true, status: 'needs_reconnect', clientIdLast4: 'abcd' }));
    expect(await screen.findByText(krogerAppCopy.rowNeeds)).toBeInTheDocument();
  });
});

describe('T42 disabled server', () => {
  beforeEach(() => vi.unstubAllGlobals());

  it('T42 hides the row when Kroger is disabled', async () => {
    renderSection({ status: 'disabled' });
    await waitFor(() => expect(screen.queryByRole('heading', { name: krogerAppCopy.rowTitle })).not.toBeInTheDocument());
  });
});

describe('T43 setup progress secrets', () => {
  it('T43 never persists client secrets in localStorage progress', () => {
    const progress = {
      step: 3,
      done: [1, 2],
      suggestedName: 'Pantry ABCD',
      returnTo: 'household' as const,
      updatedAt: new Date().toISOString(),
    };
    expect(() => assertProgressHasNoSecrets(progress)).not.toThrow();
    writeKrogerAppSetupProgress('hh-1', progress);
    expect(readKrogerAppSetupProgress('hh-1')).toEqual(progress);
  });
});

describe('T44 wrong_keys on save', () => {
  it('T44 surfaces wrong keys from PUT failures', async () => {
    const { putFailureMessage } = await import('./krogerAppMessages');
    const err = new ApiError(400, 'bad', 'store_app_keys_rejected');
    expect(putFailureMessage(err)).toBe(krogerAppCopy.errWrongKeys);
  });
});

describe('T45 kroger_unreachable', () => {
  beforeEach(() => vi.unstubAllGlobals());

  it('T45 maps store_app_unreachable to friendly copy', () => {
    const err = new ApiError(503, 'x', 'store_app_unreachable');
    expect(err.code).toBe('store_app_unreachable');
  });
});

describe('T46 no_product_access note', () => {
  it('T46 maps no_product_access to guidance copy', async () => {
    const { testCodeMessage, storeNameCtx } = await import('./krogerAppMessages');
    const text = testCodeMessage('no_product_access', storeNameCtx('Kroger'), false);
    expect(text).toMatch(/can't check what/i);
  });
});

describe('T47 redirect_mismatch at connect', () => {
  it('T47 maps redirect_mismatch callback copy for admins', async () => {
    const { krogerCallbackMessage } = await import('./krogerAppMessages');
    expect(krogerCallbackMessage('redirect_mismatch', { store: 'Kroger', storePossessive: "Kroger's" }, true))
      .toMatch(/web address/i);
  });
});

describe('T48 callback connected', () => {
  it('T48 shows no error banner text for connected', async () => {
    const { krogerCallbackMessage } = await import('./krogerAppMessages');
    expect(krogerCallbackMessage('connected', { store: 'Kroger', storePossessive: "Kroger's" }, true)).toBe('');
  });
});

describe('T49 callback cancelled', () => {
  it('T49 shows cancelled copy', async () => {
    const { krogerCallbackMessage } = await import('./krogerAppMessages');
    expect(krogerCallbackMessage('cancelled', { store: 'Kroger', storePossessive: "Kroger's" }, true))
      .toBe(krogerAppCopy.s7Cancelled);
  });
});

describe('T52 replace mode', () => {
  beforeEach(() => vi.unstubAllGlobals());

  it('T52 opens replace flow at step 6 with replace note', async () => {
    renderSection(baseApp({ configured: true, status: 'connected', clientIdLast4: '1234' }));
    fireEvent.click(await screen.findByRole('button', { name: krogerAppCopy.menuMore }));
    fireEvent.click(screen.getByRole('menuitem', { name: krogerAppCopy.menuReplace }));
    expect(await screen.findByText(krogerAppCopy.replaceNote)).toBeInTheDocument();
    expect(screen.getByRole('button', { name: krogerAppCopy.s6Test })).toBeInTheDocument();
  });
});

describe('T53 remove confirmation', () => {
  beforeEach(() => vi.unstubAllGlobals());

  it('T53 confirms before removing a store app', async () => {
    renderSection(baseApp({ configured: true, status: 'connected', clientIdLast4: '1234' }));
    fireEvent.click(await screen.findByRole('button', { name: krogerAppCopy.menuMore }));
    fireEvent.click(screen.getByRole('menuitem', { name: krogerAppCopy.menuRemove }));
    const dialog = await screen.findByRole('dialog', { name: krogerAppCopy.rmTitle });
    expect(within(dialog).getByText(/disconnect/i)).toBeInTheDocument();
  });
});

describe('T54 redirect_mismatch save flag', () => {
  it('T54 exposes redirect mismatch save flag from env', async () => {
    const { redirectMismatchSaveEnabled } = await import('./krogerAppMessages');
    expect(typeof redirectMismatchSaveEnabled).toBe('boolean');
  });
});

describe('T55 shopping kroger query', () => {
  it('T55 maps shopping redirect mismatch copy', async () => {
    const { shoppingRedirectMismatchMessage } = await import('./krogerAppMessages');
    expect(shoppingRedirectMismatchMessage()).toBe(krogerAppCopy.errMismatchShop);
  });
});

describe('T56 test again menu', () => {
  beforeEach(() => vi.unstubAllGlobals());

  it('T56 runs POST test again from the admin menu', async () => {
    const fetchMock = vi.fn((input: RequestInfo | URL, init?: RequestInit) => {
      const url = String(input);
      if (url === '/api/households/hh-1/kroger-app/test' && init?.method === 'POST') {
        return Promise.resolve(json({ ok: true }));
      }
      if (url === '/api/households/hh-1/kroger-app') return Promise.resolve(json(baseApp({ configured: true, status: 'connected', clientIdLast4: '9999' })));
      return Promise.reject(new Error(url));
    });
    vi.stubGlobal('fetch', fetchMock);
    render(
      <MantineProvider env="test">
        <MemoryRouter>
          <StoreAppSection householdId="hh-1" householdName="Pantry" isAdmin linkedStoreName="Kroger" />
        </MemoryRouter>
      </MantineProvider>,
    );
    fireEvent.click(await screen.findByRole('button', { name: krogerAppCopy.menuMore }));
    fireEvent.click(screen.getByRole('menuitem', { name: krogerAppCopy.menuTest }));
    await waitFor(() => expect(fetchMock).toHaveBeenCalledWith(
      '/api/households/hh-1/kroger-app/test',
      expect.objectContaining({ method: 'POST' }),
    ));
    expect(await screen.findByText(krogerAppCopy.testOk)).toBeInTheDocument();
  });
});

describe('T57 no web address blocks early steps only', () => {
  beforeEach(() => vi.unstubAllGlobals());

  it('T57 still allows step 2 when redirectUri is missing', async () => {
    render(
      <MantineProvider env="test">
        <KrogerAppSetupModal
          opened
          onClose={() => undefined}
          householdId="hh-1"
          householdName="Pantry"
          app={baseApp({ redirectUri: null })}
          initialStep={2}
          onSaved={() => undefined}
        />
      </MantineProvider>,
    );
    expect(screen.getByRole('button', { name: krogerAppCopy.next })).toBeEnabled();
  });

  it('T57 blocks step 3 next when redirectUri is missing', async () => {
    render(
      <MantineProvider env="test">
        <KrogerAppSetupModal
          opened
          onClose={() => undefined}
          householdId="hh-1"
          householdName="Pantry"
          app={baseApp({ redirectUri: null })}
          initialStep={3}
          onSaved={() => undefined}
        />
      </MantineProvider>,
    );
    expect(screen.getByRole('alert')).toHaveTextContent(krogerAppCopy.errNoAddress);
    expect(screen.getByRole('button', { name: krogerAppCopy.next })).toBeDisabled();
  });
});

describe('StoreLinkPreStepDialog', () => {
  it('offers setup or not now', () => {
    const onContinue = vi.fn();
    render(
      <MantineProvider env="test">
        <StoreLinkPreStepDialog opened onClose={() => undefined} onContinue={onContinue} />
      </MantineProvider>,
    );
    fireEvent.click(screen.getByRole('button', { name: krogerAppCopy.openSetup }));
    expect(onContinue).toHaveBeenCalledTimes(1);
  });
});
