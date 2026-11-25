// User-facing copy for barcodeless products (issue #421).
export const FIND_ITEM_TITLE = 'Find an item';
export const ADD_ITEM_TITLE = 'Add an item';
export const FIND_ITEM_BUTTON = 'Find an item';
export const ADD_ITEM_BUTTON = 'Add an item';

export const FIND_ITEM_INPUT_LABEL = 'Find an item';
export const TYPE_A_NAME_PLACEHOLDER = 'Type a name';
export const QUERY_TOO_SHORT = 'Type at least 2 letters.';

export const SCANS_ADD_TO_STOCK = 'Scans add to stock';
export const SCANS_REMOVE_FROM_STOCK = 'Scans remove from stock';

export const ADD_ITEM_SUBMIT = 'Add item';
export const ADD_AND_STOCK_IN = 'Add and stock in';
export const ENTER_NAME = 'Enter a name.';
export const SIZE_PLACEHOLDER = 'e.g. 16 oz';

export const SEARCH_FAILURE = "Couldn't search. Try again.";
export const SAVE_FAILURE = "Couldn't save that. Try again.";

export const NONE_ON_HAND_TITLE = 'None on hand';
export const BACK_BUTTON = 'Back';

export function nothingCalledYet(query: string): string {
  return `Nothing called "${query}" yet.`;
}

export function findNoneOnHandBody(name: string): string {
  return `You have 0 of ${name} on hand. If you had one anyway, this adds it and takes it out in one step.`;
}

export function addedOneToast(name: string): string {
  return `Added 1 ${name}.`;
}

export function tookOutOneToast(name: string): string {
  return `Took out 1 ${name}.`;
}

export function addedAndTookOutOneToast(name: string): string {
  return `Added and took out 1 ${name}.`;
}

export function addedItemToast(name: string): string {
  return `Added ${name}.`;
}

export function addNamedProductLabel(name: string): string {
  return `Add "${name}"`;
}
