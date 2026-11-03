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

const tree = (opened: boolean, a: KrogerApp, replaceMode = false) => (
  <MantineProvider env="test">
    <KrogerAppSetupModal
      opened={opened}
      onClose={() => undefined}
      householdId="hh-s"
      householdName="Pantry"
      app={a}
      replaceMode={replaceMode}
      initialStep={opened && !a.configured ? 6 : undefined}
      onSaved={() => undefined}
    />
  </MantineProvider>
);

const replaceTree = (opened: boolean, replaceMode: boolean) => tree(opened, saved, replaceMode);

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

describe('store app secret clearing', () => {
  beforeEach(() => {
    localStorage.clear();
    vi.unstubAllGlobals();
    stubPut();
  });

  it('S1: save clears the secret before step 6 back navigation shows an empty field', async () => {
    render(tree(true, app()));
    await saveKeys();
    expect(screen.queryByDisplayValue(SECRET)).not.toBeInTheDocument();
    const secret = (await backToStep6()) as HTMLInputElement;
    expect(secret.value).toBe('');
    expect(document.body.innerHTML).not.toContain(SECRET);
  });

  it('S2: returning to step 6 via step nav clears a typed secret without save', async () => {
    render(replaceTree(true, true));
    fireEvent.change(await screen.findByLabelText(/App secret/i), { target: { value: SECRET } });
    const secret = (await backToStep6()) as HTMLInputElement;
    expect(secret.value).toBe('');
    expect(document.body.innerHTML).not.toContain(SECRET);
  });

  it('S3: type secret in Replace keys, close unsaved, reopen at step 7, nav to 6: field empty', async () => {
    const view = render(replaceTree(true, true));
    fireEvent.change(await screen.findByLabelText(/App secret/i), { target: { value: SECRET } });
    view.rerender(replaceTree(false, true));
    view.rerender(replaceTree(true, false));
    await waitFor(() => expect(screen.getByRole('dialog')).toBeInTheDocument());
    const secret = (await backToStep6()) as HTMLInputElement;
    expect(secret.value).toBe('');
    expect(document.body.innerHTML).not.toContain(SECRET);
  });

  it('reopening a configured app at step 7 clears typed credentials without saving first', async () => {
    const typedId = 'typed-client-id-before-close';
    const view = render(tree(true, app()));
    fireEvent.change(screen.getByLabelText(/App ID/i), { target: { value: typedId } });
    fireEvent.change(screen.getByLabelText(/App secret/i), { target: { value: SECRET } });
    view.rerender(tree(false, app()));
    view.rerender(tree(true, saved));
    await waitFor(() => expect(screen.getByRole('dialog')).toBeInTheDocument());
    expect(screen.queryByText(krogerAppCopy.s6Title)).not.toBeInTheDocument();
    expect(document.body.innerHTML).not.toContain(SECRET);
    expect(document.body.innerHTML).not.toContain(typedId);
    fireEvent.click(screen.getByRole('button', { name: krogerAppCopy.stepsDisclosure }));
    const navButtons = Array.from(document.querySelectorAll<HTMLButtonElement>('.store-app-step-nav button'));
    fireEvent.click(navButtons[5]!);
    const secret = (await screen.findByLabelText(/App secret/i)) as HTMLInputElement;
    expect(secret.value).toBe('');
    expect(document.body.innerHTML).not.toContain(SECRET);
  });
});
