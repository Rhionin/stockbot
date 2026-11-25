import { Select, Stack, TextInput } from '@mantine/core';
import { sizeUnits, type NetSizeDraft } from '@stockbot/shared/netSize';
import {
  NET_SIZE_AMOUNT_LABEL,
  NET_SIZE_PACKAGE_HINT,
  NET_SIZE_PACKAGE_LABEL,
  NET_SIZE_PACK_COUNT_HINT,
  NET_SIZE_PACK_COUNT_LABEL,
  NET_SIZE_UNIT_LABEL,
} from '@stockbot/shared/productForm';

export interface NetSizeFieldsProps {
  draft: NetSizeDraft;
  onChange: (draft: NetSizeDraft) => void;
}

export const NetSizeFields = ({ draft, onChange }: NetSizeFieldsProps) => (
  <Stack gap="xs">
    <TextInput
      size="xs"
      label={NET_SIZE_AMOUNT_LABEL}
      inputMode="decimal"
      value={draft.amount}
      onChange={(event) => onChange({ ...draft, amount: event.currentTarget.value })}
    />
    <Select
      size="xs"
      label={NET_SIZE_UNIT_LABEL}
      data={sizeUnits}
      value={draft.unit === '' ? null : draft.unit}
      onChange={(value) => onChange({ ...draft, unit: value ?? '' })}
      allowDeselect={false}
    />
    <TextInput
      size="xs"
      label={NET_SIZE_PACKAGE_LABEL}
      description={NET_SIZE_PACKAGE_HINT}
      value={draft.packageWord}
      onChange={(event) => onChange({ ...draft, packageWord: event.currentTarget.value })}
    />
    <TextInput
      size="xs"
      label={NET_SIZE_PACK_COUNT_LABEL}
      inputMode="numeric"
      description={NET_SIZE_PACK_COUNT_HINT}
      value={draft.packCount}
      onChange={(event) => onChange({ ...draft, packCount: event.currentTarget.value })}
    />
  </Stack>
);
