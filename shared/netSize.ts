export const sizeUnits = [
  { value: 'oz', label: 'oz' },
  { value: 'fl oz', label: 'fl oz' },
  { value: 'lb', label: 'lb' },
  { value: 'g', label: 'g' },
  { value: 'kg', label: 'kg' },
  { value: 'ml', label: 'ml' },
  { value: 'L', label: 'L' },
];

export interface NetSizeDraft {
  amount: string;
  unit: string;
  packageWord: string;
  packCount: string;
}

export const emptyNetSizeDraft = (): NetSizeDraft => ({
  amount: '',
  unit: '',
  packageWord: '',
  packCount: '',
});

export function sizeWrite(draft: NetSizeDraft, touched: { size: boolean; pack: boolean }) {
  const write: {
    unitOfMeasure: string;
    netAmount?: number | null;
    netUnit?: string | null;
    packCount?: number | null;
  } = { unitOfMeasure: draft.packageWord };
  if (touched.size) {
    const trimmed = draft.amount.trim();
    write.netAmount = trimmed === '' ? null : Number(trimmed);
    write.netUnit = draft.unit;
  }
  if (touched.pack) {
    const trimmed = draft.packCount.trim();
    write.packCount = trimmed === '' ? null : Number(trimmed);
  }
  return write;
}
