/** Minimal restock target shape shared by web and mobile nudge gates. */
export type RestockTarget = {
  kind: 'months' | 'amount';
  value: number;
};