import { describe, expect, it } from 'vitest';
import { inventoryItemMatchesScanLinkSearch } from './linkRowProductFilter';

const oats = { name: 'Oats', category: 'Grain' };
const rice = { name: 'Rice', category: 'Pantry' };

describe('inventoryItemMatchesScanLinkSearch', () => {
  it('matches every row when the query is empty after trim', () => {
    expect(inventoryItemMatchesScanLinkSearch('prod-oats', oats, '', '')).toBe(true);
    expect(inventoryItemMatchesScanLinkSearch('prod-oats', oats, '   ', '')).toBe(true);
  });

  it('trims the query before normalizing', () => {
    expect(inventoryItemMatchesScanLinkSearch('prod-oats', oats, '  grain  ', '')).toBe(true);
    expect(inventoryItemMatchesScanLinkSearch('prod-oats', oats, '  quinoa  ', '')).toBe(false);
  });

  it('matches name and category as one combined string, case-insensitively', () => {
    expect(inventoryItemMatchesScanLinkSearch('prod-oats', oats, 'oats', '')).toBe(true);
    expect(inventoryItemMatchesScanLinkSearch('prod-oats', oats, 'GRAIN', '')).toBe(true);
    expect(inventoryItemMatchesScanLinkSearch('prod-oats', oats, 'oats grain', '')).toBe(true);
    expect(inventoryItemMatchesScanLinkSearch('prod-rice', rice, 'grain', '')).toBe(false);
  });

  it('drops the excluded product id when exclude is non-empty', () => {
    expect(inventoryItemMatchesScanLinkSearch('prod-oats', oats, '', 'prod-oats')).toBe(false);
    expect(inventoryItemMatchesScanLinkSearch('prod-oats', oats, 'oats', 'prod-oats')).toBe(false);
    expect(inventoryItemMatchesScanLinkSearch('prod-rice', rice, 'rice', 'prod-oats')).toBe(true);
  });

  it('does not exclude when excludeProductId is empty', () => {
    expect(inventoryItemMatchesScanLinkSearch('prod-oats', oats, 'oats', '')).toBe(true);
  });
});
