import { useCallback, useEffect, useState } from 'react';
import {
  ActionIcon, Alert, Button, Group, Loader, Menu, Modal, Stack, TextInput, Title,
} from '@mantine/core';
import { getHouseholdHistory, setMoveQuantity, undoMove } from '../../api/client';
import { quantityLocked } from '../../api/contract';
import type { LedgerMove } from '../../types';
import { ProductPhoto } from '../inventory/ProductPhoto';
import {
  deltaName, deltaText, directionWord, formatMoveClock, groupMovesByDay, ledgerEmptyCopy, orderBackfillPairs, quantityPrompt, undoCopy,
} from './format';
import { PulseChart, type PulseInspect } from './PulseChart';
import '../groups/groups.css';
import '../inventory/shelf.css';
import './history.css';

type Direction = 'all' | 'in' | 'out';

// The Used response is only stock-outs, so the backfill stock-in that locks
// the matching use is loaded with the same search and no direction.
async function readLedger(direction: Direction, query: string) {
  const trimmed = query.trim();
  const q = trimmed === '' ? undefined : trimmed;
  const next = await getHouseholdHistory({
    direction: direction === 'all' ? undefined : direction,
    q,
  });
  if (direction !== 'out') return { moves: next.moves, lockMoves: next.moves, setupAt: next.setupAt };
  const wider = await getHouseholdHistory({ q });
  return { moves: next.moves, lockMoves: wider.moves, setupAt: next.setupAt };
}

const filters: { id: Direction; label: string }[] = [
  { id: 'all', label: 'All' },
  { id: 'in', label: 'Stocked in' },
  { id: 'out', label: 'Used' },
];

function KebabIcon() {
  return (
    <svg viewBox="0 0 16 16" width="16" height="16" aria-hidden="true">
      <circle cx="8" cy="3.1" r="1.35" fill="currentColor" />
      <circle cx="8" cy="8" r="1.35" fill="currentColor" />
      <circle cx="8" cy="12.9" r="1.35" fill="currentColor" />
    </svg>
  );
}

export function LedgerPage() {
  const [moves, setMoves] = useState<LedgerMove[] | null>(null);
  const [lockMoves, setLockMoves] = useState<LedgerMove[]>([]);
  const [setupAt, setSetupAt] = useState<string | undefined>();
  const [inspectedKey, setInspectedKey] = useState<string | null>(null);
  const [direction, setDirection] = useState<Direction>('all');
  const [query, setQuery] = useState('');
  const [loading, setLoading] = useState(true);
  const [error, setError] = useState('');
  const [editing, setEditing] = useState<LedgerMove | null>(null);
  const [quantity, setQuantity] = useState('1');
  const [undoing, setUndoing] = useState<LedgerMove | null>(null);
  const [pending, setPending] = useState(false);

  useEffect(() => {
    let active = true;
    void Promise.resolve().then(async () => {
      if (!active) return;
      try {
        const next = await readLedger(direction, query);
        if (!active) return;
        setMoves(next.moves);
        setLockMoves(next.lockMoves);
        setSetupAt(next.setupAt);
        setError('');
      } catch (requestError) {
        if (!active) return;
        setError(requestError instanceof Error ? requestError.message : 'Unable to load history.');
      } finally {
        if (active) setLoading(false);
      }
    });
    return () => {
      active = false;
    };
  }, [direction, query]);

  const reload = async () => {
    const next = await readLedger(direction, query);
    setMoves(next.moves);
    setLockMoves(next.lockMoves);
    setSetupAt(next.setupAt);
  };

  const onInspect = useCallback((day: PulseInspect | null) => {
    setInspectedKey(day?.key ?? null);
  }, []);

  const saveQuantity = async () => {
    if (!editing) return;
    const next = Number(quantity);
    if (!Number.isInteger(next) || next < 1) {
      setError('Quantity has to be at least 1. Undo the move to remove it.');
      return;
    }
    setPending(true);
    setError('');
    try {
      await setMoveQuantity(editing.id, next);
      setEditing(null);
      await reload();
    } catch (requestError) {
      setError(requestError instanceof Error ? requestError.message : 'Unable to change that quantity.');
    } finally {
      setPending(false);
    }
  };

  const confirmUndo = async () => {
    if (!undoing) return;
    setPending(true);
    setError('');
    try {
      await undoMove(undoing.id);
      setUndoing(null);
      await reload();
    } catch (requestError) {
      setError(requestError instanceof Error ? requestError.message : 'Unable to undo that move.');
    } finally {
      setPending(false);
    }
  };

  const now = new Date();
  const groups = groupMovesByDay(orderBackfillPairs(moves ?? []), now);
  const undo = undoing ? undoCopy(undoing.direction, undoing.quantity, quantityLocked(undoing, lockMoves)) : null;
  const showEmpty = !loading && error === '' && moves !== null && groups.length === 0;

  return (
    <Stack className="bin-page ledger-page" gap={0}>
      <Title order={1} size="h3" className="ledger-title">History</Title>
      <div className="ledger-tools">
        <TextInput
          className="ledger-search"
          label="Search products or groups"
          value={query}
          onChange={(event) => setQuery(event.currentTarget.value)}
          autoComplete="off"
        />
        <div className="history-filters" role="group" aria-label="Which moves to show">
          {filters.map((item) => (
            <button
              key={item.id}
              type="button"
              className="history-filter"
              aria-pressed={direction === item.id}
              onClick={() => setDirection(item.id)}
            >
              {item.label}
            </button>
          ))}
        </div>
      </div>
      {error === '' && moves !== null && moves.length > 0 && (
        <PulseChart moves={moves} now={now} setupAt={setupAt} onInspect={onInspect} />
      )}
      {loading && moves === null && <Loader aria-label="Loading history" />}
      {error !== '' && editing === null && undoing === null && <Alert color="red">{error}</Alert>}
      {showEmpty && <p className="ledger-empty">{ledgerEmptyCopy(direction, query)}</p>}
      {error === '' && groups.length > 0 && (
        <div className="ledger-days">
          {groups.map((day) => (
            <section
              key={day.key}
              className={day.key === inspectedKey ? 'ledger-day is-inspected' : 'ledger-day'}
              aria-labelledby={`ledger-day-${day.key}`}
            >
              <h2 id={`ledger-day-${day.key}`} className="ledger-day-title">{day.heading}</h2>
              <ol className="ledger-moves">
                {day.moves.map((move) => {
                  const clock = formatMoveClock(new Date(move.at), move.source);
                  const word = directionWord(move.direction);
                  return (
                    <li key={move.id} className="ledger-move">
                      <span className="ledger-photo">
                        <ProductPhoto src={move.imageUrl} name={move.productName} decorative />
                      </span>
                      <div className="ledger-copy">
                        <p className="ledger-name">{move.productName}</p>
                        <p className="ledger-when">{clock}</p>
                        {move.groupName && <p className="ledger-group">{move.groupName}</p>}
                      </div>
                      <span className={move.direction === 'in' ? 'ledger-amount is-in' : 'ledger-amount is-out'}>
                        <span className="ledger-figure">{deltaText(move.direction, move.quantity)}</span>
                        <span className="ledger-word">{word}</span>
                      </span>
                      <Menu position="bottom-end" withinPortal withInitialFocusPlaceholder={false} transitionProps={{ duration: 0 }}>
                        <Menu.Target>
                          <ActionIcon
                            variant="subtle"
                            className="history-menu"
                            aria-label={`Actions for ${move.productName}, ${deltaName(move.direction, move.quantity)}, ${clock}`}
                          >
                            <KebabIcon />
                          </ActionIcon>
                        </Menu.Target>
                        <Menu.Dropdown>
                          {!quantityLocked(move, lockMoves) && (
                            <Menu.Item onClick={() => { setQuantity(String(move.quantity)); setError(''); setEditing(move); }}>
                              Change quantity
                            </Menu.Item>
                          )}
                          <Menu.Item onClick={() => { setError(''); setUndoing(move); }}>
                            {move.direction === 'in' ? 'Undo this stock in' : 'Undo this use'}
                          </Menu.Item>
                        </Menu.Dropdown>
                      </Menu>
                    </li>
                  );
                })}
              </ol>
            </section>
          ))}
        </div>
      )}
      <Modal
        opened={editing !== null}
        onClose={() => { if (!pending) setEditing(null); }}
        title="Change quantity"
        closeButtonProps={{ 'aria-label': 'Close' }}
      >
        {editing && (
          <Stack gap="sm">
            <TextInput
              className="history-quantity"
              type="number"
              inputMode="numeric"
              min={1}
              max={999}
              label={quantityPrompt(editing.direction)}
              value={quantity}
              onChange={(event) => setQuantity(event.currentTarget.value)}
            />
            {error !== '' && <Alert color="red">{error}</Alert>}
            <Button loading={pending} onClick={() => void saveQuantity()}>Save quantity</Button>
          </Stack>
        )}
      </Modal>
      <Modal
        opened={undoing !== null}
        onClose={() => { if (!pending) setUndoing(null); }}
        title={undo?.title ?? 'Undo this move?'}
        closeButtonProps={{ 'aria-label': 'Close' }}
      >
        {undo && (
          <Stack gap="sm">
            <p className="ledger-empty">{undo.body}</p>
            {error !== '' && <Alert color="red">{error}</Alert>}
            <Group gap="sm" grow>
              <Button variant="default" disabled={pending} onClick={() => setUndoing(null)}>Keep it</Button>
              <Button color="red" loading={pending} onClick={() => void confirmUndo()}>{undo.confirm}</Button>
            </Group>
          </Stack>
        )}
      </Modal>
    </Stack>
  );
}
