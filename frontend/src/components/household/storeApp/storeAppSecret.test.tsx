import { fireEvent, render, screen, waitFor } from '@testing-library/react';
import { MantineProvider } from '@mantine/core';
import { beforeEach, describe, expect, it, vi } from 'vitest';
import { krogerAppCopy } from '@stockbot/shared/krogerAppCopy';
import type { KrogerApp } from '@stockbot/shared/types';
import { KrogerAppSetupModal } from './KrogerAppSetupModal';

const SECRET = 'tester-fake-secret-9f3c';

const json = (body: unknown, status = 200) => new Response(JSON.stringify(body), {
  status,
  headers: { 'Content-Type': 'application/json' },
});

const app = (o: Partial<KrogerApp> = {}): KrogerApp => ({
  configured: false,
  clientIdLast4: null,
  redirectUri: 'https://app.example/cb',
  modality: 'PICKUP',
  productAccess: 'unknown',
  status: 'not_set_up',
  canManage: true,
  ...o,
});

const saved = app({ configured: true, status: 'ready_to_connect', clientIdLast4: '1234' });

function stubPut() {
  vi.stubGlobal('fetch', vi.fn((input: RequestInfo | URL, init?: RequestInit) => {
    const url = String(input);
    if (url === '/api/households/hh-s/kroger-app' && init?.method === 'PUT') {
      return Promise.resolve(json({ ...saved, test: { ok: true } }));
    }
    return Promise.reject(new Error(`${init?.method ?? 'GET'} ${url}`));
  }));
}

const tree = (opened: boolean, a: KrogerApp) => (
  <MantineProvider env="test">
    <KrogerAppSetupModal
      opened={opened}
      onClose={() => undefined}
      householdId="hh-s"
      householdName="Pantry"
      app={a}
      initialStep={opened && !a.configured ? 6 : undefined}
      onSaved={() => undefined}
    />
  </MantineProvider>
);

async function saveKeys() {
  fireEvent.change(screen.getByLabelText(/App ID/i), { target: { value: 'fake-client-id-1234' } });
  fireEvent.change(screen.getByLabelText(/App secret/i), { target: { value: SECRET } });
  fireEvent.click(screen.getByRole('button', { name: krogerAppCopy.s6Test }));
  expect(await screen.findByText(krogerAppCopy.s6Ok)).toBeInTheDocument();
}

async function backToStep6() {
  fireEvent.click(screen.getByRole('button', { name: krogerAppCopy.stepsDisclosure }));
  const navButtons = Array.from(document.querySelectorAll<HTMLButtonElement>('.store-app-step-nav button'));
  expect(navButtons).toHaveLength(7);
  expect(navButtons[5]!.disabled).toBe(false);
  fireEvent.click(navButtons[5]!);
  return screen.findByLabelText(/App secret/i);
}

describe('store app secret is not retained after save', () => {
  beforeEach(() => {
    localStorage.clear();
    vi.unstubAllGlobals();
    stubPut();
  });

  it('step nav back to step 6 after save shows an empty secret field', async () => {
    render(tree(true, app()));
    await saveKeys();
    const secret = (await backToStep6()) as HTMLInputElement;
    expect(secret.value).toBe('');
    expect(document.body.innerHTML).not.toContain(SECRET);
  });

  it('close and reopen after save does not bring the secret back', async () => {
    const view = render(tree(true, app()));
    await saveKeys();
    view.rerender(tree(false, saved));
    view.rerender(tree(true, saved));
    await waitFor(() => expect(screen.getByRole('dialog')).toBeInTheDocument());
    const secret = (await backToStep6()) as HTMLInputElement;
    expect(secret.value).toBe('');
    expect(document.body.innerHTML).not.toContain(SECRET);
  });

  it('opening a configured app at step 7 keeps the secret empty when navigating to step 6', async () => {
    render(
      <MantineProvider env="test">
        <KrogerAppSetupModal
          opened
          onClose={() => undefined}
          householdId="hh-s"
          householdName="Pantry"
          app={saved}
          onSaved={() => undefined}
        />
      </MantineProvider>,
    );
    await waitFor(() => expect(screen.getByRole('dialog')).toBeInTheDocument());
    fireEvent.click(screen.getByRole('button', { name: krogerAppCopy.stepsDisclosure }));
    const navButtons = Array.from(document.querySelectorAll<HTMLButtonElement>('.store-app-step-nav button'));
    fireEvent.click(navButtons[5]!);
    const secret = (await screen.findByLabelText(/App secret/i)) as HTMLInputElement;
    expect(secret.value).toBe('');
    expect(document.body.innerHTML).not.toContain(SECRET);
  });
});
