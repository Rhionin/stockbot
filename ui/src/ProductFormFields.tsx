import { forwardRef } from 'react';
import type { TextFieldHandle } from './TextField';
import {
  PRODUCT_BRAND_LABEL,
  PRODUCT_NAME_LABEL,
  withSize,
  type ProductDraft,
  type ProductDraftErrors,
} from '@stockbot/shared/productForm';
import { YStack } from '@tamagui/stacks';
import { ContributeFields, type ContributeFieldsProps } from './ContributeFields';
import { NetSizeFields } from './NetSizeFields';
import { TextField } from './TextField';

export type ProductFormFieldsProps = {
  draft: ProductDraft;
  errors: ProductDraftErrors;
  disabled?: boolean;
  idPrefix?: string;
  autoFocusName?: boolean;
  fontRegular: string;
  fontBold: string;
  onChange: (draft: ProductDraft) => void;
  contribute: Omit<ContributeFieldsProps, 'choice' | 'onChoice' | 'disabled' | 'fontRegular' | 'fontBold'>;
};

export const ProductFormFields = forwardRef<TextFieldHandle, ProductFormFieldsProps>(function ProductFormFields(
  { draft, errors, disabled = false, idPrefix = 'product', autoFocusName = false, fontRegular, fontBold, onChange, contribute },
  nameRef,
) {
  return (
    <YStack gap={16}>
      <TextField
        ref={nameRef}
        fieldId={`${idPrefix}-name`}
        label={PRODUCT_NAME_LABEL}
        required
        value={draft.name}
        error={errors.name}
        disabled={disabled}
        autoFocus={autoFocusName}
        fontRegular={fontRegular}
        fontBold={fontBold}
        onChangeText={(name) => onChange({ ...draft, name })}
      />
      <TextField
        fieldId={`${idPrefix}-brand`}
        label={PRODUCT_BRAND_LABEL}
        value={draft.brand}
        disabled={disabled}
        fontRegular={fontRegular}
        fontBold={fontBold}
        onChangeText={(brand) => onChange({ ...draft, brand })}
      />
      <NetSizeFields
        idPrefix={`${idPrefix}-size`}
        draft={draft.size}
        sizeError={errors.size}
        packCountError={errors.packCount}
        disabled={disabled}
        fontRegular={fontRegular}
        fontBold={fontBold}
        onChange={(size) => onChange(withSize(draft, size))}
      />
      <ContributeFields
        {...contribute}
        choice={draft.share}
        disabled={disabled}
        fontRegular={fontRegular}
        fontBold={fontBold}
        onChoice={(share) => onChange({ ...draft, share })}
      />
    </YStack>
  );
});
