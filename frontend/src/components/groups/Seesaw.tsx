import { useEffect, useId, useRef, useState, type Ref } from 'react';
import { Button, Group, NumberInput, Text } from '@mantine/core';
import type { GroupTarget, ProductGroup } from '../../types';
import { clampMonth, type SeesawDraft, type SeesawEnd, type SeesawPin, type SeesawView } from './seesaw';

// Material Symbols "keep" (push pin), Apache-2.0.
function PinIcon() {
  return (
    <svg className="seesaw-pin" viewBox="0 0 24 24" aria-hidden="true">
      <path
        fill="currentColor"
        d="M16 12V4h1V2H7v2h1v8l-2 2v2h5.2v6h1.6v-6H18v-2l-2-2z"
      />
    </svg>
  );
}

function Pill({
  end,
  onOpen,
  buttonRef,
}: {
  end: SeesawEnd;
  onOpen: (pin: SeesawPin) => void;
  buttonRef?: Ref<HTMLButtonElement>;
}) {
  const descriptionId = useId();
  const visibleName = end.isDefault ? `${end.label} default` : end.label;
  return (
    <>
      <button
        ref={buttonRef}
        type="button"
        className={end.pinned ? 'seesaw-pill is-pinned' : 'seesaw-pill is-inferred'}
        aria-pressed={end.pinned}
        aria-label={visibleName}
        aria-describedby={descriptionId}
        onClick={() => onOpen(end.pin)}
      >
        {end.pinned && <PinIcon />}
        <span>{end.label}</span>
        {end.isDefault && (
          // The space is in the name ("3 mo default"). nowrap collapses it,
          // so the flex gap stays the only space on screen.
          <span className="seesaw-default">{' '}default</span>
        )}
      </button>
      <span id={descriptionId} className="seesaw-description">{end.description}</span>
    </>
  );
}

export function Seesaw({
  view,
  group,
  draft,
  onDraft,
  onSave,
}: {
  view: SeesawView;
  group: Pick<ProductGroup, 'quantity' | 'windowMonths'>;
  draft: SeesawDraft | null;
  onDraft: (draft: SeesawDraft | null) => void;
  onSave: (target: GroupTarget) => Promise<void>;
}) {
  const [error, setError] = useState('');
  const amountRef = useRef<HTMLButtonElement>(null);
  const timeRef = useRef<HTMLButtonElement>(null);
  const restore = useRef<SeesawPin | null>(null);
  const amountUnit = view.dimension === 'volume' ? 'Fluid ounces' : 'Ounces';
  const canClear = group.quantity !== undefined || group.windowMonths !== undefined;

  useEffect(() => {
    if (draft !== null) return;
    const pin = restore.current;
    if (pin === null) return;
    restore.current = null;
    (pin === 'amount' ? amountRef : timeRef).current?.focus();
  }, [draft]);

  const open = (pin: SeesawPin) => {
    if (draft?.pin === pin) return;
    const value = pin === 'amount'
      ? (view.amountValue !== null ? Math.round(view.amountValue * 10) / 10 : '')
      : (view.monthsValue !== null ? clampMonth(view.monthsValue) : '');
    setError('');
    onDraft({ pin, value });
  };

  const closeEditor = () => {
    if (draft) restore.current = draft.pin;
    setError('');
    onDraft(null);
  };

  const cancel = () => {
    closeEditor();
  };

  const save = async () => {
    if (!draft) return;
    setError('');
    try {
      if (draft.pin === 'amount') {
        const value = typeof draft.value === 'number' ? draft.value : Number(draft.value);
        if (!Number.isFinite(value) || value <= 0 || value > 999) {
          setError('Enter an amount greater than zero and at most 999 ounces.');
          return;
        }
        await onSave({ quantity: value, dimension: view.dimension });
      } else {
        const value = typeof draft.value === 'number' ? draft.value : Number(draft.value);
        if (!Number.isInteger(value) || value < 1 || value > 12) {
          setError('Enter a whole number of months from 1 to 12.');
          return;
        }
        await onSave({ windowMonths: value });
      }
      closeEditor();
    } catch (requestError) {
      setError(requestError instanceof Error ? requestError.message : 'Unable to save the target.');
    }
  };

  const clear = async () => {
    setError('');
    try {
      await onSave({ clear: true });
      closeEditor();
    } catch (requestError) {
      setError(requestError instanceof Error ? requestError.message : 'Unable to save the target.');
    }
  };

  const editor = (pin: SeesawPin) => (
    <NumberInput
      className="seesaw-editor"
      autoFocus
      aria-label={pin === 'amount' ? amountUnit : 'Months'}
      value={draft?.value ?? ''}
      onChange={(value) => onDraft({ pin, value })}
      min={pin === 'time' ? 1 : 0}
      max={pin === 'time' ? 12 : 999}
      allowDecimal={pin === 'amount'}
      decimalScale={pin === 'amount' ? 1 : 0}
      hideControls
      size="sm"
      onKeyDown={(event) => {
        if (event.key === 'Escape') {
          event.preventDefault();
          cancel();
        }
      }}
    />
  );

  const unknownAtEnd = !view.time.known;

  return (
    <form
      className="seesaw"
      onSubmit={(event) => {
        event.preventDefault();
        void save();
      }}
    >
      <p className="seesaw-caption">KEEP ON HAND</p>
      <div className="seesaw-row">
        <div className="seesaw-end">
          {draft?.pin === 'amount' ? editor('amount') : <Pill end={view.amount} onOpen={open} buttonRef={amountRef} />}
        </div>
        <div className="seesaw-beam">
          <span className="seesaw-beam-line" />
          {view.rateLabel !== '' && <span className="seesaw-rate">{view.rateLabel}</span>}
        </div>
        <div className="seesaw-end">
          {draft?.pin === 'time' ? editor('time') : <Pill end={view.time} onOpen={open} buttonRef={timeRef} />}
        </div>
      </div>
      {view.unknownHint !== '' && (
        <p className={unknownAtEnd ? 'seesaw-unknown is-end' : 'seesaw-unknown'}>{view.unknownHint}</p>
      )}
      {draft === null && view.unknownHint === '' && (
        <p className="seesaw-hint">Tap either end to set it; the other follows</p>
      )}
      {draft !== null && (
        <Group className="seesaw-actions" gap="sm">
          <Button type="submit" size="sm">Save</Button>
          <Button type="button" size="sm" variant="default" onClick={cancel}>Cancel</Button>
          {draft.pin === 'time' && canClear && (
            <Button type="button" size="sm" variant="subtle" onClick={() => void clear()}>
              Use the household default
            </Button>
          )}
        </Group>
      )}
      {error !== '' && <Text className="seesaw-error" size="sm" c="red">{error}</Text>}
    </form>
  );
}
