import { useCallback, useEffect, useRef, useState } from 'react';
import { ActivityIndicator, Pressable, StyleSheet, Text, View } from 'react-native';
import { ChoiceList, DateField, NumberStepper } from '@stockbot/ui';
import type { ItemInstanceWithStatus, ScanEntry } from '@stockbot/shared/types';
import {
  CLEARED_EXPIRY_ISO,
  EXPIRY_ADD_LABEL,
  EXPIRY_CANCEL_LABEL,
  EXPIRY_CHANGE_LABEL,
  EXPIRY_CLEAR_LABEL,
  EXPIRY_INVALID,
  EXPIRY_LABEL,
  EXPIRY_SAVE_FAILURE,
  expiryDatePart,
  formatExpirySummary,
  formatInstanceLabel,
  INSTANCE_AUTOMATIC_LABEL,
  INSTANCE_PICKER_HINT,
  INSTANCE_PICKER_LABEL,
  INSTANCES_LOAD_FAILURE,
  INSTANCES_LOADING,
  INSTANCES_NONE,
  parseExpiryDraft,
  parseUnitCountText,
  shouldApplyServerUnitCount,
  UNIT_COUNT_DECREASE_LABEL,
  UNIT_COUNT_INCREASE_LABEL,
  UNIT_COUNT_INVALID,
  UNIT_COUNT_LABEL,
  UNIT_COUNT_SAVE_DELAY_MS,
  UNIT_COUNT_SAVE_FAILURE,
} from '@stockbot/shared/cardDetails';
import { inlineFailure, isStorageFailureCode, ServerNoticeError, UnreachableError } from '@stockbot/shared/contract';
import { isValidUnitCount, sortInstancesUseOldestFirst } from '@stockbot/shared/queueUtils';
import { ApiError, type StockbotClient } from '../api/client';
import { colors, fonts } from '../ui/theme';

// The offline and server notices have their own banner, so a save held by one
// shows no sentence beside the control.
function heldByBanner(error: unknown): boolean {
  return error instanceof UnreachableError || error instanceof ServerNoticeError;
}

export interface SaveHandlers {
  onChanged: () => void;
  onStorageFailure: (code: string, message: string) => void;
}

function reportedStorage(error: unknown, handlers: SaveHandlers): boolean {
  if (!(error instanceof ApiError) || !isStorageFailureCode(error.code)) return false;
  handlers.onStorageFailure(error.code, error.message);
  return true;
}

export type Flush = () => Promise<boolean>;

function useLatest<T>(value: T) {
  const ref = useRef(value);
  useEffect(() => {
    ref.current = value;
  });
  return ref;
}

function useUnitCount(client: StockbotClient, entry: ScanEntry, handlers: SaveHandlers) {
  const [text, setText] = useState(String(entry.unitCount));
  const [error, setError] = useState('');
  const valueRef = useRef(entry.unitCount);
  const savedRef = useRef(entry.unitCount);
  const invalidRef = useRef(false);
  const timer = useRef<ReturnType<typeof setTimeout> | null>(null);
  const generation = useRef(0);
  const handlersRef = useLatest(handlers);

  const clearTimer = useCallback(() => {
    if (timer.current !== null) clearTimeout(timer.current);
    timer.current = null;
  }, []);

  useEffect(() => clearTimer, [clearTimer]);

  const revert = useCallback(() => {
    valueRef.current = savedRef.current;
    invalidRef.current = false;
    setText(String(savedRef.current));
  }, []);

  const persist = useCallback(async (draft: number): Promise<boolean> => {
    clearTimer();
    let sending = draft;
    for (;;) {
      if (sending === savedRef.current) return true;
      const current = ++generation.current;
      try {
        await client.updateScan(entry.id, { unitCount: sending });
      } catch (requestError) {
        if (current !== generation.current || valueRef.current !== sending) return false;
        revert();
        if (reportedStorage(requestError, handlersRef.current) || heldByBanner(requestError)) return false;
        setError(UNIT_COUNT_SAVE_FAILURE);
        return false;
      }
      if (current !== generation.current) return true;
      savedRef.current = sending;
      // A newer tap landed while this save was in flight. Keep that number and
      // send it next, so the response never puts the older count back.
      if (valueRef.current !== sending) {
        sending = valueRef.current;
        continue;
      }
      setError('');
      handlersRef.current.onChanged();
      return true;
    }
  }, [client, clearTimer, entry.id, handlersRef, revert]);

  const schedule = useCallback(() => {
    clearTimer();
    timer.current = setTimeout(() => {
      timer.current = null;
      void persist(valueRef.current);
    }, UNIT_COUNT_SAVE_DELAY_MS);
  }, [clearTimer, persist]);

  useEffect(() => {
    if (invalidRef.current) {
      valueRef.current = entry.unitCount;
      savedRef.current = entry.unitCount;
      return;
    }
    if (!shouldApplyServerUnitCount(valueRef.current, savedRef.current, entry.unitCount)) return;
    valueRef.current = entry.unitCount;
    savedRef.current = entry.unitCount;
    setText(String(entry.unitCount));
  }, [entry.unitCount]);

  const change = (next: string) => {
    setText(next);
    const parsed = parseUnitCountText(next);
    if (parsed === null) {
      invalidRef.current = true;
      clearTimer();
      setError(next.trim() === '' ? '' : UNIT_COUNT_INVALID);
      return;
    }
    invalidRef.current = false;
    valueRef.current = parsed;
    setError('');
    schedule();
  };

  const step = (delta: 1 | -1) => {
    const next = valueRef.current + delta;
    if (!isValidUnitCount(next)) return;
    invalidRef.current = false;
    valueRef.current = next;
    setText(String(next));
    setError('');
    schedule();
  };

  const commit = () => {
    if (invalidRef.current) {
      const blank = text.trim() === '';
      revert();
      setError(blank ? '' : UNIT_COUNT_INVALID);
    }
    void persist(valueRef.current);
  };

  // False when Confirm must not go ahead on a count the card cannot save.
  const flush: Flush = async () => {
    clearTimer();
    if (invalidRef.current) {
      setError(UNIT_COUNT_INVALID);
      return false;
    }
    return persist(valueRef.current);
  };

  const shown = parseUnitCountText(text);
  return { text, error, change, step, commit, flush, canDecrease: shown !== null && shown > 1 };
}

function useExpiry(client: StockbotClient, entry: ScanEntry, handlers: SaveHandlers) {
  const [committed, setCommitted] = useState(expiryDatePart(entry.expiresAt));
  const [draft, setDraft] = useState(committed);
  const [editing, setEditing] = useState(false);
  const [error, setError] = useState('');
  const [seenExpiresAt, setSeenExpiresAt] = useState(entry.expiresAt);
  const generation = useRef(0);
  const handlersRef = useLatest(handlers);

  // A date the person is still typing is not replaced by a live update.
  if (seenExpiresAt !== entry.expiresAt) {
    const next = expiryDatePart(entry.expiresAt);
    setSeenExpiresAt(entry.expiresAt);
    setCommitted(next);
    if (!(editing && draft !== committed)) setDraft(next);
  }

  const save = async (value: string, afterSave: () => void) => {
    const parsed = parseExpiryDraft(value);
    if (parsed.kind === 'invalid') {
      setError(EXPIRY_INVALID);
      return;
    }
    const current = ++generation.current;
    try {
      await client.updateScan(entry.id, { expiresAt: parsed.kind === 'clear' ? CLEARED_EXPIRY_ISO : parsed.iso });
    } catch (requestError) {
      if (current !== generation.current) return;
      setDraft(committed);
      if (reportedStorage(requestError, handlersRef.current) || heldByBanner(requestError)) return;
      setError(EXPIRY_SAVE_FAILURE);
      return;
    }
    if (current !== generation.current) return;
    setCommitted(value.trim());
    setDraft(value.trim());
    setError('');
    afterSave();
    handlersRef.current.onChanged();
  };

  const open = () => {
    setError('');
    setEditing(true);
  };

  const cancel = () => {
    setDraft(committed);
    setError('');
    setEditing(false);
  };

  const change = (next: string) => {
    setDraft(next);
    setError('');
  };

  const commit = () => {
    if (!editing) return;
    if (draft === committed) {
      setEditing(false);
      return;
    }
    void save(draft, () => setEditing(false));
  };

  const clear = () => {
    setError('');
    void save('', () => setEditing(false));
  };

  return { committed, draft, editing, error, open, cancel, change, commit, clear };
}

export function InstancePicker({
  client,
  itemId,
  value,
  reloadKey,
  onChange,
}: {
  client: StockbotClient;
  itemId: string;
  value: string;
  reloadKey: number;
  onChange: (instanceId: string) => void;
}) {
  const [instances, setInstances] = useState<ItemInstanceWithStatus[]>([]);
  const [loading, setLoading] = useState(true);
  const [error, setError] = useState('');

  useEffect(() => {
    let active = true;
    // A later reload replaces a unit the household already took out.
    // eslint-disable-next-line react-hooks/set-state-in-effect
    setLoading(true);
    setError('');
    client.listInstances(itemId)
      .then((rows) => {
        if (active) setInstances(sortInstancesUseOldestFirst(rows));
      })
      .catch((requestError: unknown) => {
        if (active) setError(inlineFailure(requestError, INSTANCES_LOAD_FAILURE) ?? '');
      })
      .finally(() => {
        if (active) setLoading(false);
      });
    return () => {
      active = false;
    };
  }, [client, itemId, reloadKey]);

  if (loading) {
    return <ActivityIndicator accessibilityLabel={INSTANCES_LOADING} color={colors.shelfDeep} style={styles.loader} />;
  }
  if (error !== '') return <Text accessibilityRole="alert" style={styles.error}>{error}</Text>;
  if (instances.length === 0) return <Text style={styles.muted}>{INSTANCES_NONE}</Text>;
  return (
    <ChoiceList
      testID={`instances-${itemId}`}
      label={INSTANCE_PICKER_LABEL}
      description={INSTANCE_PICKER_HINT}
      choices={[
        { value: '', label: INSTANCE_AUTOMATIC_LABEL },
        ...instances.map((instance) => ({ value: instance.id, label: formatInstanceLabel(instance) })),
      ]}
      value={value}
      fontRegular={fonts.regular}
      fontBold={fonts.bold}
      onChange={onChange}
    />
  );
}

export interface CardDetailsProps extends SaveHandlers {
  client: StockbotClient;
  entry: ScanEntry;
  blocked: boolean;
  linkOfferOpen: boolean;
  itemId: string | undefined;
  instanceId: string;
  instancesOpen: boolean;
  instanceReload: number;
  busy: boolean;
  register: (entryId: string, flush: Flush | null) => void;
  onInstanceChange: (instanceId: string) => void;
  onToggleInstances: () => void;
}

export function CardDetails({
  client,
  entry,
  blocked,
  linkOfferOpen,
  itemId,
  instanceId,
  instancesOpen,
  instanceReload,
  busy,
  register,
  onInstanceChange,
  onToggleInstances,
  onChanged,
  onStorageFailure,
}: CardDetailsProps) {
  const handlers = { onChanged, onStorageFailure };
  const unit = useUnitCount(client, entry, handlers);
  const expiry = useExpiry(client, entry, handlers);
  const flushRef = useLatest(unit.flush);

  useEffect(() => {
    register(entry.id, () => flushRef.current());
    return () => register(entry.id, null);
  }, [entry.id, flushRef, register]);

  const stockIn = entry.direction === 'stock_in';
  const stockOut = entry.direction === 'stock_out';
  const unitError = unit.error !== '';
  const picking = stockOut && !blocked && !linkOfferOpen && itemId !== undefined;

  return (
    <View testID={`details-${entry.id}`} style={styles.details}>
      <View style={styles.row}>
        <NumberStepper
          testID={`stepper-${entry.id}`}
          value={unit.text}
          label={UNIT_COUNT_LABEL}
          decreaseLabel={UNIT_COUNT_DECREASE_LABEL}
          increaseLabel={UNIT_COUNT_INCREASE_LABEL}
          canDecrease={unit.canDecrease}
          invalid={unitError}
          disabled={busy}
          fontBold={fonts.bold}
          onChangeText={unit.change}
          onStep={unit.step}
          onCommit={unit.commit}
        />
        {stockIn && (expiry.editing ? (
          <View style={styles.row}>
            <DateField
              testID={`expiry-field-${entry.id}`}
              value={expiry.draft}
              label={EXPIRY_LABEL}
              invalid={expiry.error === EXPIRY_INVALID}
              autoFocus
              fontRegular={fonts.regular}
              onChangeText={expiry.change}
              onCommit={expiry.commit}
            />
            <TextButton label={EXPIRY_CANCEL_LABEL} text="Cancel" onPress={expiry.cancel} />
          </View>
        ) : expiry.committed !== '' ? (
          <View style={styles.row}>
            <Text style={styles.summary}>{formatExpirySummary(expiry.committed)}</Text>
            <TextButton label={EXPIRY_CHANGE_LABEL} text="Change" disabled={busy} onPress={expiry.open} />
            <TextButton label={EXPIRY_CLEAR_LABEL} text="Clear" disabled={busy} onPress={expiry.clear} />
          </View>
        ) : (
          <TextButton label={EXPIRY_ADD_LABEL} text={EXPIRY_ADD_LABEL} disabled={busy} onPress={expiry.open} />
        ))}
        {picking && (
          <Pressable
            accessibilityRole="button"
            accessibilityLabel="More"
            accessibilityState={{ expanded: instancesOpen }}
            aria-expanded={instancesOpen}
            onPress={onToggleInstances}
            style={styles.textButton}
          >
            <Text style={styles.textButtonText}>More</Text>
          </Pressable>
        )}
      </View>
      {unitError && <Text accessibilityRole="alert" style={styles.error}>{unit.error}</Text>}
      {expiry.error !== '' && <Text accessibilityRole="alert" style={styles.error}>{expiry.error}</Text>}
      {picking && instancesOpen && (
        <View style={styles.picker}>
          <InstancePicker client={client} itemId={itemId} value={instanceId} reloadKey={instanceReload} onChange={onInstanceChange} />
        </View>
      )}
    </View>
  );
}

function TextButton({
  label,
  text,
  disabled = false,
  onPress,
}: {
  label: string;
  text: string;
  disabled?: boolean;
  onPress: () => void;
}) {
  return (
    <Pressable
      accessibilityRole="button"
      accessibilityLabel={label}
      accessibilityState={{ disabled }}
      disabled={disabled}
      onPress={onPress}
      style={styles.textButton}
    >
      <Text style={styles.textButtonText}>{text}</Text>
    </Pressable>
  );
}

const styles = StyleSheet.create({
  details: {
    marginTop: 12,
    gap: 8,
  },
  row: {
    flexDirection: 'row',
    flexWrap: 'wrap',
    alignItems: 'center',
    gap: 8,
  },
  picker: {
    borderTopWidth: 1,
    borderTopColor: colors.rule,
    paddingTop: 10,
  },
  summary: {
    fontFamily: fonts.regular,
    fontSize: 16,
    lineHeight: 22,
    color: colors.ink,
  },
  textButton: {
    minHeight: 44,
    minWidth: 44,
    paddingHorizontal: 8,
    justifyContent: 'center',
  },
  textButtonText: {
    fontFamily: fonts.bold,
    fontSize: 16,
    color: colors.shelfDeep,
    textDecorationLine: 'underline',
  },
  error: {
    fontFamily: fonts.regular,
    fontSize: 16,
    lineHeight: 22,
    color: colors.danger,
  },
  muted: {
    fontFamily: fonts.regular,
    fontSize: 16,
    lineHeight: 22,
    color: colors.inkMuted,
  },
  loader: {
    alignSelf: 'flex-start',
    minHeight: 44,
  },
});
