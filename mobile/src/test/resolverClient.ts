import type { StockbotClient } from '../api/client';

type ResolverMethods = Pick<
  StockbotClient,
  | 'lookupProduct'
  | 'searchProducts'
  | 'createProduct'
  | 'createProductOverride'
  | 'getContributionSettings'
  | 'updateContributionSettings'
  | 'listLinkSuggestions'
>;

export function resolverClientStubs(overrides: Partial<ResolverMethods> = {}): ResolverMethods {
  return {
    lookupProduct: jest.fn(async () => ({ product: null, source: '' })),
    searchProducts: jest.fn(async () => []),
    createProduct: jest.fn(async () => {
      throw new Error('createProduct not stubbed');
    }),
    createProductOverride: jest.fn(async () => undefined),
    getContributionSettings: jest.fn(async () => ({ enabled: false, configured: false })),
    updateContributionSettings: jest.fn(async (enabled: boolean) => ({ enabled, configured: false })),
    listLinkSuggestions: jest.fn(async () => ({ suggestions: [] })),
    ...overrides,
  };
}
