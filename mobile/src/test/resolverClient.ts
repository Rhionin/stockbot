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
  | 'ensureProductItem'
  | 'addItemInstance'
  | 'removeItemInstance'
  | 'stockOutItem'
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
    ensureProductItem: jest.fn(async (productId: string) => ({ itemId: `item-${productId}` })),
    addItemInstance: jest.fn(async (itemId: string) => ({
      id: 'instance-1',
      itemId,
      stockInAt: '2026-10-16T12:00:00Z',
      expiresAt: null,
      removedAt: null,
      removalReason: null,
      createdAt: '2026-10-16T12:00:00Z',
    })),
    removeItemInstance: jest.fn(async () => undefined),
    stockOutItem: jest.fn(async () => ({ stockOutMoveId: 'move-out-1' })),
    ...overrides,
  };
}
