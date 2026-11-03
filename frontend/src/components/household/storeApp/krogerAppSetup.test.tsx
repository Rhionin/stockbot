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

function apiPath(input: RequestInfo | URL): string {
  const raw = String(input);
  try {
    return new URL(raw, 'http://stockbot.test').pathname;
  } catch {
    return raw.split('?')[0] ?? raw;
  }
}

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
    const url = apiPath(input);
    const method = init?.method ?? 'GET';
    if (url === `/api/households/${householdId}/kroger-app` && method === 'GET') {
      return Promise.resolve(json(app));
    }
    if (url === `/api/households/${householdId}/kroger-app/test` && method === 'POST') {
      return Promise.resolve(json({ ok: true }));
    }
    if (url === `/api/households/${householdId}/kroger-app` && method === 'DELETE') {
      return Promise.resolve(json(baseApp()));
    }
    if (url.startsWith('/api/providers/kroger/authorize')) {
      return Promise.resolve(json({ authorizationUrl: 'https://kroger.example/oauth' }));
    }
    if (url === '/api/providers' && method === 'GET') {
      return Promise.resolve(json([{
        id: 'kroger',
        displayName: 'Kroger',
        capabilities: {
          auth: 'oauth2_authorization_code',
          delivery: 'server_push',
          confirmation: 'per_request',
          mutation: 'add_only',
          identity: 'derived',
        },
        connectionState: 'disconnected',
        credentialsConfigured: true,
      }]));
    }
    return Promise.reject(new Error(`${method} ${url}`));
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
    expect(await screen.findByRole('button', { name: krogerAppCopy.rowSetUp })).toBeInTheDocument();
    expect(screen.getByRole('heading', { level: 3, name: krogerAppCopy.rowTitle })).toBeInTheDocument();
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
  beforeEach(() => vi.unstubAllGlobals());

  it('T44 surfaces wrong keys in the setup modal without server text', async () => {
    const householdId = 'hh-1';
    vi.stubGlobal('fetch', vi.fn((input: RequestInfo | URL, init?: RequestInit) => {
      const url = apiPath(input);
      const method = init?.method ?? 'GET';
      if (url === `/api/households/${householdId}/kroger-app` && method === 'PUT') {
        return Promise.resolve(json({ code: 'store_app_keys_rejected', error: 'RAW-SERVER' }, 400));
      }
      if (url === `/api/households/${householdId}/kroger-app` && method === 'GET') {
        return Promise.resolve(json(baseApp()));
      }
      return Promise.reject(new Error(`${method} ${url}`));
    }));
    render(
      <MantineProvider env="test">
        <KrogerAppSetupModal
          opened
          onClose={() => undefined}
          householdId={householdId}
          householdName="Pantry"
          app={baseApp()}
          initialStep={6}
          onSaved={() => undefined}
        />
      </MantineProvider>,
    );
    fireEvent.change(screen.getByLabelText(/App ID/i), { target: { value: 'id123' } });
    fireEvent.change(screen.getByLabelText(/App secret/i), { target: { value: 'secret456' } });
    fireEvent.click(screen.getByRole('button', { name: krogerAppCopy.s6Test }));
    expect(await screen.findByText(krogerAppCopy.errWrongKeys)).toBeInTheDocument();
    expect(screen.queryByText(/RAW-SERVER/)).not.toBeInTheDocument();
  });
});

describe('T45 kroger_unreachable', () => {
  it('T45 maps store_app_unreachable to friendly copy', async () => {
    const { mapKrogerAppApiError } = await import('./krogerAppMessages');
    const err = new ApiError(503, 'RAW-503', 'store_app_unreachable');
    const mapped = mapKrogerAppApiError(err);
    expect(mapped).toMatchObject({ kind: 'message', message: krogerAppCopy.errUnreachable, alert: true });
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
    expect(screen.getByText(krogerAppCopy.errNoAddress)).toBeInTheDocument();
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

describe('kroger app vitest mutants', () => {
  beforeEach(() => {
    localStorage.clear();
    vi.unstubAllGlobals();
  });

  it('M2 sends PUT to save keys', async () => {
    const householdId = 'hh-m2';
    const fetchMock = vi.fn((input: RequestInfo | URL, init?: RequestInit) => {
      const url = String(input);
      if (url === `/api/households/${householdId}/kroger-app` && init?.method === 'PUT') {
        return Promise.resolve(json({
          ...baseApp({ configured: true, status: 'ready_to_connect', clientIdLast4: '9999' }),
          test: { ok: true },
        }));
      }
      if (url === `/api/households/${householdId}/kroger-app`) {
        return Promise.resolve(json(baseApp()));
      }
      return Promise.reject(new Error(`${init?.method ?? 'GET'} ${url}`));
    });
    vi.stubGlobal('fetch', fetchMock);
    render(
      <MantineProvider env="test">
        <KrogerAppSetupModal
          opened
          onClose={() => undefined}
          householdId={householdId}
          householdName="Pantry"
          app={baseApp()}
          initialStep={6}
          onSaved={() => undefined}
        />
      </MantineProvider>,
    );
    fireEvent.change(screen.getByLabelText(/App ID/i), { target: { value: 'abc' } });
    fireEvent.change(screen.getByLabelText(/App secret/i), { target: { value: 'def' } });
    fireEvent.click(screen.getByRole('button', { name: krogerAppCopy.s6Test }));
    await waitFor(() => expect(fetchMock).toHaveBeenCalledWith(
      `/api/households/${householdId}/kroger-app`,
      expect.objectContaining({ method: 'PUT' }),
    ));
  });

  it('M3 shows modal errors with role=alert', async () => {
    const householdId = 'hh-m3';
    vi.stubGlobal('fetch', vi.fn((input: RequestInfo | URL, init?: RequestInit) => {
      const url = String(input);
      if (url === `/api/households/${householdId}/kroger-app` && init?.method === 'PUT') {
        return Promise.resolve(json({ code: 'store_app_keys_rejected', error: 'x' }, 400));
      }
      if (url === `/api/households/${householdId}/kroger-app`) return Promise.resolve(json(baseApp()));
      return Promise.reject(new Error(url));
    }));
    render(
      <MantineProvider env="test">
        <KrogerAppSetupModal
          opened
          onClose={() => undefined}
          householdId={householdId}
          householdName="Pantry"
          app={baseApp()}
          initialStep={6}
          onSaved={() => undefined}
        />
      </MantineProvider>,
    );
    fireEvent.change(screen.getByLabelText(/App ID/i), { target: { value: 'a' } });
    fireEvent.change(screen.getByLabelText(/App secret/i), { target: { value: 'b' } });
    fireEvent.click(screen.getByRole('button', { name: krogerAppCopy.s6Test }));
    expect(await screen.findByRole('alert')).toHaveTextContent(krogerAppCopy.errWrongKeys);
  });

  it('M4 marks Test and save busy and keeps key fields read-only while saving', async () => {
    const householdId = 'hh-m4-busy';
    let resolvePut: (value: Response) => void = () => undefined;
    const putPending = new Promise<Response>((resolve) => {
      resolvePut = resolve;
    });
    vi.stubGlobal('fetch', vi.fn((input: RequestInfo | URL, init?: RequestInit) => {
      const url = String(input);
      if (url === `/api/households/${householdId}/kroger-app` && init?.method === 'PUT') {
        return putPending;
      }
      if (url === `/api/households/${householdId}/kroger-app`) return Promise.resolve(json(baseApp()));
      return Promise.reject(new Error(url));
    }));
    render(
      <MantineProvider env="test">
        <KrogerAppSetupModal
          opened
          onClose={() => undefined}
          householdId={householdId}
          householdName="Pantry"
          app={baseApp()}
          initialStep={6}
          onSaved={() => undefined}
        />
      </MantineProvider>,
    );
    fireEvent.change(screen.getByLabelText(/App ID/i), { target: { value: 'abc' } });
    fireEvent.change(screen.getByLabelText(/App secret/i), { target: { value: 'def' } });
    fireEvent.click(screen.getByRole('button', { name: krogerAppCopy.s6Test }));
    await waitFor(() => {
      expect(screen.getByRole('button', { name: krogerAppCopy.s6Testing })).toHaveAttribute('aria-busy', 'true');
    });
    expect(screen.getByLabelText(/App ID/i)).toHaveAttribute('readonly');
    resolvePut(json({
      ...baseApp({ configured: true, status: 'ready_to_connect' }),
      test: { ok: true },
    }));
    await waitFor(() => expect(screen.getByText(krogerAppCopy.s6Ok)).toBeInTheDocument());
  });

  it('M4 shows a loading skeleton while the row loads', async () => {
    const householdId = 'hh-m4';
    vi.stubGlobal('fetch', vi.fn((input: RequestInfo | URL) => {
      const url = String(input);
      if (url === `/api/households/${householdId}/kroger-app`) {
        return new Promise(() => undefined);
      }
      return Promise.reject(new Error(url));
    }));
    render(
      <MantineProvider env="test">
        <MemoryRouter>
          <StoreAppSection householdId={householdId} householdName="Pantry" isAdmin linkedStoreName="Kroger" />
        </MemoryRouter>
      </MantineProvider>,
    );
    expect(screen.getByRole('heading', { name: krogerAppCopy.rowTitle }).closest('section')).toHaveAttribute('aria-busy', 'true');
  });

  it('M5 hides the row when the server disables the store app', async () => {
    renderSection({ status: 'disabled' });
    await waitFor(() => expect(screen.queryByRole('heading', { name: krogerAppCopy.rowTitle })).not.toBeInTheDocument());
  });

  it('M6 deletes with the kroger-app route', async () => {
    const fetchMock = vi.fn((input: RequestInfo | URL, init?: RequestInit) => {
      const url = String(input);
      if (url === '/api/households/hh-1/kroger-app' && init?.method === 'DELETE') {
        return Promise.resolve(json(baseApp()));
      }
      if (url === '/api/households/hh-1/kroger-app') {
        return Promise.resolve(json(baseApp({ configured: true, status: 'connected', clientIdLast4: '1234' })));
      }
      if (url === '/api/households/hh-1/kroger-app/test' && init?.method === 'POST') {
        return Promise.resolve(json({ ok: true }));
      }
      return Promise.reject(new Error(`${init?.method ?? 'GET'} ${url}`));
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
    fireEvent.click(screen.getByRole('menuitem', { name: krogerAppCopy.menuRemove }));
    fireEvent.click(await screen.findByRole('button', { name: krogerAppCopy.rmConfirm }));
    await waitFor(() => expect(fetchMock).toHaveBeenCalledWith(
      '/api/households/hh-1/kroger-app',
      expect.objectContaining({ method: 'DELETE' }),
    ));
  });

  it('M7 trims pasted keys before PUT', async () => {
    const householdId = 'hh-m7';
    const fetchMock = vi.fn((input: RequestInfo | URL, init?: RequestInit) => {
      const url = String(input);
      if (url === `/api/households/${householdId}/kroger-app` && init?.method === 'PUT') {
        const body = JSON.parse(String(init.body)) as { clientId: string; clientSecret: string };
        expect(body.clientId).toBe('trimmed-id');
        expect(body.clientSecret).toBe('trimmed-secret');
        return Promise.resolve(json({
          ...baseApp({ configured: true, status: 'ready_to_connect' }),
          test: { ok: true },
        }));
      }
      if (url === `/api/households/${householdId}/kroger-app`) return Promise.resolve(json(baseApp()));
      return Promise.reject(new Error(url));
    });
    vi.stubGlobal('fetch', fetchMock);
    render(
      <MantineProvider env="test">
        <KrogerAppSetupModal
          opened
          onClose={() => undefined}
          householdId={householdId}
          householdName="Pantry"
          app={baseApp()}
          initialStep={6}
          onSaved={() => undefined}
        />
      </MantineProvider>,
    );
    fireEvent.change(screen.getByLabelText(/App ID/i), { target: { value: '  trimmed-id\n' } });
    fireEvent.change(screen.getByLabelText(/App secret/i), { target: { value: ' trimmed-secret ' } });
    fireEvent.click(screen.getByRole('button', { name: krogerAppCopy.s6Test }));
    await waitFor(() => expect(fetchMock).toHaveBeenCalled());
    const putCall = fetchMock.mock.calls.find((call) => {
      const init = call[1] as RequestInit | undefined;
      return apiPath(call[0] as string) === `/api/households/${householdId}/kroger-app` && init?.method === 'PUT';
    });
    expect(putCall).toBeDefined();
    expect(JSON.parse(String((putCall?.[1] as RequestInit).body))).toEqual({
      clientId: 'trimmed-id',
      clientSecret: 'trimmed-secret',
      modality: 'PICKUP',
    });
  });

  it('M8 does not treat a failed test as success', async () => {
    const householdId = 'hh-m8';
    vi.stubGlobal('fetch', vi.fn((input: RequestInfo | URL, init?: RequestInit) => {
      const url = String(input);
      if (url === `/api/households/${householdId}/kroger-app` && init?.method === 'PUT') {
        return Promise.resolve(json({
          ...baseApp({ configured: true, status: 'not_set_up' }),
          test: { ok: false, code: 'wrong_keys' },
        }));
      }
      if (url === `/api/households/${householdId}/kroger-app`) return Promise.resolve(json(baseApp()));
      return Promise.reject(new Error(url));
    }));
    render(
      <MantineProvider env="test">
        <KrogerAppSetupModal
          opened
          onClose={() => undefined}
          householdId={householdId}
          householdName="Pantry"
          app={baseApp()}
          initialStep={6}
          onSaved={() => undefined}
        />
      </MantineProvider>,
    );
    fireEvent.change(screen.getByLabelText(/App ID/i), { target: { value: 'a' } });
    fireEvent.change(screen.getByLabelText(/App secret/i), { target: { value: 'b' } });
    fireEvent.click(screen.getByRole('button', { name: krogerAppCopy.s6Test }));
    expect(await screen.findByText(krogerAppCopy.errWrongKeys)).toBeInTheDocument();
    expect(screen.queryByText(krogerAppCopy.s6Ok)).not.toBeInTheDocument();
  });

  it('B3 shows Your keys work on step 7 after save', async () => {
    const householdId = 'hh-b3';
    vi.stubGlobal('fetch', vi.fn((input: RequestInfo | URL, init?: RequestInit) => {
      const url = String(input);
      if (url === `/api/households/${householdId}/kroger-app` && init?.method === 'PUT') {
        return Promise.resolve(json({
          ...baseApp({ configured: true, status: 'ready_to_connect', clientIdLast4: '9999' }),
          test: { ok: true },
        }));
      }
      if (url === `/api/households/${householdId}/kroger-app`) return Promise.resolve(json(baseApp()));
      return Promise.reject(new Error(url));
    }));
    render(
      <MantineProvider env="test">
        <KrogerAppSetupModal
          opened
          onClose={() => undefined}
          householdId={householdId}
          householdName="Pantry"
          linkedStoreName="Kroger"
          app={baseApp()}
          initialStep={6}
          onSaved={() => undefined}
        />
      </MantineProvider>,
    );
    fireEvent.change(screen.getByLabelText(/App ID/i), { target: { value: 'abc' } });
    fireEvent.change(screen.getByLabelText(/App secret/i), { target: { value: 'def' } });
    fireEvent.click(screen.getByRole('button', { name: krogerAppCopy.s6Test }));
    expect(await screen.findByRole('status')).toHaveTextContent(krogerAppCopy.s6Ok);
    expect(screen.getByRole('button', { name: /Connect your Kroger account/i })).toBeInTheDocument();
  });

  it('T37 switches to member view after forbidden_role on save', async () => {
    const householdId = 'hh-t37';
    writeKrogerAppSetupProgress(householdId, {
      step: 6,
      done: [1, 2, 3, 4, 5],
      suggestedName: 'Pantry',
      returnTo: 'household',
      updatedAt: new Date().toISOString(),
    });
    vi.stubGlobal('fetch', vi.fn((input: RequestInfo | URL, init?: RequestInit) => {
      const url = String(input);
      if (url === `/api/households/${householdId}/kroger-app` && init?.method === 'PUT') {
        return Promise.resolve(json({ code: 'forbidden_role', error: 'Only an owner can do that.' }, 403));
      }
      if (url === `/api/households/${householdId}/kroger-app`) {
        return Promise.resolve(json(baseApp({ status: 'not_set_up', canManage: true })));
      }
      return Promise.reject(new Error(url));
    }));
    render(
      <MantineProvider env="test">
        <MemoryRouter>
          <StoreAppSection householdId={householdId} householdName="Pantry" isAdmin linkedStoreName="Kroger" />
        </MemoryRouter>
      </MantineProvider>,
    );
    fireEvent.click(await screen.findByRole('button', { name: krogerAppCopy.rowSetUp }));
    fireEvent.change(await screen.findByLabelText(/App ID/i), { target: { value: 'a' } });
    fireEvent.change(screen.getByLabelText(/App secret/i), { target: { value: 'b' } });
    fireEvent.click(screen.getByRole('button', { name: krogerAppCopy.s6Test }));
    await waitFor(() => expect(screen.queryByRole('dialog')).not.toBeInTheDocument());
    await screen.findByText(krogerAppCopy.askAdmin);
  });
});

describe('B11 kroger-app contract (merged #532)', () => {
  beforeEach(() => {
    localStorage.clear();
    vi.unstubAllGlobals();
  });

  it('(1) hides the store app row when GET /api/providers lists Kroger as disabled only', async () => {
    const householdId = 'hh-b11-1';
    vi.stubGlobal('fetch', vi.fn((input: RequestInfo | URL, init?: RequestInit) => {
      const url = apiPath(input);
      const method = init?.method ?? 'GET';
      if (url === `/api/households/${householdId}/kroger-app` && method === 'GET') {
        return Promise.resolve(json(baseApp({ configured: true, status: 'connected', clientIdLast4: '1234' })));
      }
      if (url === '/api/providers' && method === 'GET') {
        return Promise.resolve(json([{ id: 'kroger', displayName: 'Kroger', status: 'disabled' }]));
      }
      return Promise.reject(new Error(`${method} ${url}`));
    }));
    render(
      <MantineProvider env="test">
        <MemoryRouter>
          <StoreAppSection householdId={householdId} householdName="Pantry" isAdmin linkedStoreName="Kroger" />
        </MemoryRouter>
      </MantineProvider>,
    );
    await waitFor(() => expect(screen.queryByRole('heading', { name: krogerAppCopy.rowTitle })).not.toBeInTheDocument());
  });

  it('(2) maps forbidden_role on test again to the member view without store_disabled copy', async () => {
    const householdId = 'hh-b11-2';
    vi.stubGlobal('fetch', vi.fn((input: RequestInfo | URL, init?: RequestInit) => {
      const url = apiPath(input);
      const method = init?.method ?? 'GET';
      if (url === `/api/households/${householdId}/kroger-app/test` && method === 'POST') {
        return Promise.resolve(json({ code: 'forbidden_role', error: 'Only an owner can do that.' }, 403));
      }
      if (url === `/api/households/${householdId}/kroger-app` && method === 'GET') {
        return Promise.resolve(json(baseApp({ configured: true, status: 'connected', clientIdLast4: '1234' })));
      }
      if (url === '/api/providers' && method === 'GET') {
        return Promise.resolve(json([{
          id: 'kroger',
          displayName: 'Kroger',
          capabilities: { auth: 'oauth2_authorization_code', delivery: 'server_push', confirmation: 'per_request', mutation: 'add_only', identity: 'derived' },
          connectionState: 'disconnected',
          credentialsConfigured: true,
        }]));
      }
      return Promise.reject(new Error(`${method} ${url}`));
    }));
    render(
      <MantineProvider env="test">
        <MemoryRouter>
          <StoreAppSection householdId={householdId} householdName="Pantry" isAdmin linkedStoreName="Kroger" />
        </MemoryRouter>
      </MantineProvider>,
    );
    fireEvent.click(await screen.findByRole('button', { name: krogerAppCopy.menuMore }));
    fireEvent.click(screen.getByRole('menuitem', { name: krogerAppCopy.menuTest }));
    await waitFor(() => {
      expect(screen.queryByRole('button', { name: krogerAppCopy.menuMore })).not.toBeInTheDocument();
    });
    expect(screen.queryByText(/Online store features are turned off/i)).not.toBeInTheDocument();
  });

  it('(3) rate_limited on test again shows spec copy and keeps Test again enabled', async () => {
    const householdId = 'hh-b11-3';
    vi.stubGlobal('fetch', vi.fn((input: RequestInfo | URL, init?: RequestInit) => {
      const url = apiPath(input);
      const method = init?.method ?? 'GET';
      if (url === `/api/households/${householdId}/kroger-app/test` && method === 'POST') {
        return Promise.resolve(json({ code: 'rate_limited', error: 'Too many tries.' }, 429));
      }
      if (url === `/api/households/${householdId}/kroger-app` && method === 'GET') {
        return Promise.resolve(json(baseApp({ configured: true, status: 'connected', clientIdLast4: '1234', productAccess: 'unknown' })));
      }
      if (url === '/api/providers' && method === 'GET') {
        return Promise.resolve(json([{
          id: 'kroger',
          displayName: 'Kroger',
          capabilities: { auth: 'oauth2_authorization_code', delivery: 'server_push', confirmation: 'per_request', mutation: 'add_only', identity: 'derived' },
          connectionState: 'disconnected',
          credentialsConfigured: true,
        }]));
      }
      return Promise.reject(new Error(`${method} ${url}`));
    }));
    render(
      <MantineProvider env="test">
        <MemoryRouter>
          <StoreAppSection householdId={householdId} householdName="Pantry" isAdmin linkedStoreName="Kroger" />
        </MemoryRouter>
      </MantineProvider>,
    );
    fireEvent.click(await screen.findByRole('button', { name: krogerAppCopy.menuMore }));
    fireEvent.click(screen.getByRole('menuitem', { name: krogerAppCopy.menuTest }));
    const notice = await screen.findByText(krogerAppCopy.errTooMany);
    expect(notice).toHaveClass('store-app-note-unknown');
    expect(screen.getByRole('button', { name: krogerAppCopy.menuTest })).not.toBeDisabled();
  });

  it('(3) rate_limited on save shows spec copy in the setup modal', async () => {
    const householdId = 'hh-b11-4';
    vi.stubGlobal('fetch', vi.fn((input: RequestInfo | URL, init?: RequestInit) => {
      const url = apiPath(input);
      const method = init?.method ?? 'GET';
      if (url === `/api/households/${householdId}/kroger-app` && method === 'PUT') {
        return Promise.resolve(json({ code: 'rate_limited', error: 'Too many tries.' }, 429));
      }
      if (url === `/api/households/${householdId}/kroger-app` && method === 'GET') {
        return Promise.resolve(json(baseApp()));
      }
      return Promise.reject(new Error(`${method} ${url}`));
    }));
    render(
      <MantineProvider env="test">
        <KrogerAppSetupModal
          opened
          onClose={() => undefined}
          householdId={householdId}
          householdName="Pantry"
          app={baseApp()}
          initialStep={6}
          onSaved={() => undefined}
        />
      </MantineProvider>,
    );
    fireEvent.change(screen.getByLabelText(/App ID/i), { target: { value: 'abc' } });
    fireEvent.change(screen.getByLabelText(/App secret/i), { target: { value: 'def' } });
    fireEvent.click(screen.getByRole('button', { name: krogerAppCopy.s6Test }));
    const notice = await screen.findByText(krogerAppCopy.errTooMany);
    expect(notice).toHaveClass('store-app-note-unknown');
    expect(screen.queryByRole('alert')).not.toBeInTheDocument();
  });
});

describe('modality picker (c.app.modality)', () => {
  beforeEach(() => {
    localStorage.clear();
    vi.unstubAllGlobals();
  });

  function renderStep6(app: KrogerApp, options: { replaceMode?: boolean } = {}) {
    const householdId = 'hh-modality';
    vi.stubGlobal('fetch', vi.fn((input: RequestInfo | URL, init?: RequestInit) => {
      const url = apiPath(input);
      const method = init?.method ?? 'GET';
      if (url === `/api/households/${householdId}/kroger-app` && method === 'GET') {
        return Promise.resolve(json(app));
      }
      return Promise.reject(new Error(`${method} ${url}`));
    }));
    render(
      <MantineProvider env="test">
        <KrogerAppSetupModal
          opened
          onClose={() => undefined}
          householdId={householdId}
          householdName="Pantry"
          linkedStoreName="Kroger"
          app={app}
          replaceMode={options.replaceMode}
          initialStep={6}
          onSaved={() => undefined}
        />
      </MantineProvider>,
    );
  }

  it('defaults to Pickup on first setup', () => {
    renderStep6(baseApp());
    expect(screen.getByRole('radio', { name: krogerAppCopy.modalityOptionPickup })).toBeChecked();
  });

  it('preselects saved modality on Replace keys', () => {
    renderStep6(baseApp({ configured: true, modality: 'DELIVERY', clientIdLast4: '1234' }), { replaceMode: true });
    expect(screen.getByRole('radio', { name: krogerAppCopy.modalityOptionDelivery })).toBeChecked();
  });

  it('never shows raw enum values to the user', () => {
    renderStep6(baseApp());
    expect(screen.queryByText('PICKUP')).not.toBeInTheDocument();
    expect(screen.queryByText('DELIVERY')).not.toBeInTheDocument();
    expect(screen.queryByText(/modality/i)).not.toBeInTheDocument();
  });

  it('saves modality-only without re-testing keys when the app is already configured', async () => {
    const householdId = 'hh-modality-save';
    const fetchMock = vi.fn((input: RequestInfo | URL, init?: RequestInit) => {
      const url = apiPath(input);
      const method = init?.method ?? 'GET';
      if (url === `/api/households/${householdId}/kroger-app` && method === 'PUT') {
        const body = JSON.parse(String(init?.body)) as Record<string, string>;
        expect(body).toEqual({ modality: 'DELIVERY' });
        return Promise.resolve(json({
          ...baseApp({ configured: true, modality: 'DELIVERY' }),
          test: { ok: true },
        }));
      }
      if (url === `/api/households/${householdId}/kroger-app/test` && method === 'POST') {
        throw new Error('modality-only save must not call test');
      }
      if (url === `/api/households/${householdId}/kroger-app` && method === 'GET') {
        return Promise.resolve(json(baseApp({ configured: true, modality: 'PICKUP', clientIdLast4: '1234' })));
      }
      return Promise.reject(new Error(`${method} ${url}`));
    });
    vi.stubGlobal('fetch', fetchMock);
    render(
      <MantineProvider env="test">
        <KrogerAppSetupModal
          opened
          onClose={() => undefined}
          householdId={householdId}
          householdName="Pantry"
          app={baseApp({ configured: true, modality: 'PICKUP', clientIdLast4: '1234' })}
          initialStep={6}
          onSaved={() => undefined}
        />
      </MantineProvider>,
    );
    fireEvent.click(screen.getByRole('radio', { name: krogerAppCopy.modalityOptionDelivery }));
    await waitFor(() => expect(fetchMock).toHaveBeenCalledWith(
      `/api/households/${householdId}/kroger-app`,
      expect.objectContaining({ method: 'PUT' }),
    ));
  });

  it('keeps the selected option and shows an inline error when modality save fails', async () => {
    const householdId = 'hh-modality-fail';
    vi.stubGlobal('fetch', vi.fn((input: RequestInfo | URL, init?: RequestInit) => {
      const url = apiPath(input);
      const method = init?.method ?? 'GET';
      if (url === `/api/households/${householdId}/kroger-app` && method === 'PUT') {
        return Promise.resolve(json({ code: 'validation', error: 'bad' }, 400));
      }
      if (url === `/api/households/${householdId}/kroger-app` && method === 'GET') {
        return Promise.resolve(json(baseApp({ configured: true, modality: 'PICKUP', clientIdLast4: '1234' })));
      }
      return Promise.reject(new Error(`${method} ${url}`));
    }));
    render(
      <MantineProvider env="test">
        <KrogerAppSetupModal
          opened
          onClose={() => undefined}
          householdId={householdId}
          householdName="Pantry"
          app={baseApp({ configured: true, modality: 'PICKUP', clientIdLast4: '1234' })}
          initialStep={6}
          onSaved={() => undefined}
        />
      </MantineProvider>,
    );
    fireEvent.click(screen.getByRole('radio', { name: krogerAppCopy.modalityOptionDelivery }));
    expect(await screen.findByRole('alert')).toHaveTextContent(krogerAppCopy.vInvalid);
    expect(screen.getByRole('radio', { name: krogerAppCopy.modalityOptionDelivery })).toBeChecked();
  });
});
