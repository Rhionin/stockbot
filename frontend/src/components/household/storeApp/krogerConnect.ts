import { authorizeProvider } from '../../../api/client';

export async function startKrogerConnect(returnTo: 'household' | 'shopping'): Promise<void> {
  const { authorizationUrl } = await authorizeProvider('kroger', returnTo);
  window.location.assign(authorizationUrl);
}
