import { useEffect, useRef, useState } from 'react';
import { Alert, NativeSelect, NumberInput, Stack } from '@mantine/core';
import { getSupplyOverride, setSupplyOverride } from '../../api/client';
import { inlineFailure } from '@stockbot/shared/contract';

type SupplyChoice = 'default' | 'months' | 'quantity';

export interface SupplyOverrideProps {
  productId: string;
}

export const SupplyOverride = ({ productId }: SupplyOverrideProps) => {
  const [choice, setChoice] = useState<SupplyChoice>('default');
  const [months, setMonths] = useState<number | string>(3);
  const [quantity, setQuantity] = useState<number | string>(1);
  const [error, setError] = useState('');
  // Blur can run before the next render sees the typed value.
  const monthsRef = useRef(months);
  const quantityRef = useRef(quantity);

  useEffect(() => {
    let active = true;
    void getSupplyOverride(productId)
      .then((override) => {
        if (!active) return;
        if (override.windowMonths !== undefined) {
          setChoice('months');
          monthsRef.current = override.windowMonths;
          setMonths(override.windowMonths);
          return;
        }
        if (override.quantity !== undefined) {
          setChoice('quantity');
          quantityRef.current = override.quantity;
          setQuantity(override.quantity);
        }
      })
      .catch((requestError: unknown) => {
        if (active) {
          setError(inlineFailure(requestError, 'Unable to load the supply override.') ?? '');
        }
      });
    return () => {
      active = false;
    };
  }, [productId]);

  const save = async (next: SupplyChoice, windowMonths: number, units: number) => {
    setError('');
    try {
      if (next === 'default') {
        await setSupplyOverride(productId, { clear: true });
        return;
      }
      if (next === 'months') {
        await setSupplyOverride(productId, { windowMonths });
        return;
      }
      await setSupplyOverride(productId, { quantity: units });
    } catch (requestError) {
      setError(inlineFailure(requestError, 'Unable to save the supply override.') ?? '');
    }
  };

  const monthsValue = typeof months === 'number' ? months : Number(months);
  const quantityValue = typeof quantity === 'number' ? quantity : Number(quantity);

  return (
    <Stack gap="xs">
      <NativeSelect
        size="xs"
        label="Supply"
        value={choice}
        data={[
          { value: 'default', label: 'Account default' },
          { value: 'months', label: 'Months of supply' },
          { value: 'quantity', label: 'Quantity on hand' },
        ]}
        onChange={(event) => {
          const next = event.currentTarget.value as SupplyChoice;
          setChoice(next);
          if (next === 'default') {
            void save('default', monthsValue, quantityValue);
          }
        }}
      />
      {choice === 'months' && (
        <NumberInput
          size="xs"
          label="Months"
          min={1}
          max={12}
          allowDecimal={false}
          value={months}
          onChange={(value) => {
            monthsRef.current = value;
            setMonths(value);
          }}
          onBlur={() => {
            const typed = typeof monthsRef.current === 'number' ? monthsRef.current : Number(monthsRef.current);
            if (Number.isInteger(typed) && typed >= 1 && typed <= 12) {
              void save('months', typed, quantityValue);
            }
          }}
        />
      )}
      {choice === 'quantity' && (
        <NumberInput
          size="xs"
          label="Quantity"
          min={1}
          max={999}
          allowDecimal={false}
          value={quantity}
          onChange={(value) => {
            quantityRef.current = value;
            setQuantity(value);
          }}
          onBlur={() => {
            const typed = typeof quantityRef.current === 'number' ? quantityRef.current : Number(quantityRef.current);
            if (Number.isInteger(typed) && typed >= 1 && typed <= 999) {
              void save('quantity', monthsValue, typed);
            }
          }}
        />
      )}
      {error !== '' && <Alert color="red" py="xs">{error}</Alert>}
    </Stack>
  );
};
