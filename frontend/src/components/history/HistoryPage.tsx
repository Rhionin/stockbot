import { useCallback, useEffect, useState } from 'react';
import { Link, useParams } from 'react-router-dom';
import {
  ActionIcon, Alert, Anchor, Button, Group, Loader, Menu, Modal, Stack, TextInput,
} from '@mantine/core';
import { getGroupHistory, getItemHistory, setMoveQuantity, undoMove } from '../../api/client';
import { quantityLocked } from '../../api/contract';
import type { HistoryMove, HistoryView } from '../../types';
import {
  daysLeftLabel, deltaName, deltaText, formatMoveWhen, formatPaceNumber, moveTitle, onHandLabel, quantityPrompt, trendLabel, undoCopy,
} from './format';
import '../groups/groups.css';
import './history.css';

type Filter = 'recent' | 'in' | 'out';

const filters: { id: Filter; label: string }[] = [
  { id: 'recent', label: 'Recent' },
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

function Sparkline({ values }: { values: number[] }) {
  if (values.length < 2 || values.every((value) => value === 0)) return null;
  const max = Math.max(...values, 1);
  const width = 100;
  const height = 36;
  const step = width / (values.length - 1);
  const points = values.map((value, index) => {
    const x = index * step;
    const y = height - 2 - (value / max) * (height - 4);
    return `${x},${y}`;
  }).join(' ');
  return (
    <svg className="history-spark" viewBox={`0 0 ${width} ${height}`} preserveAspectRatio="none" aria-hidden="true">
      <polyline points={points} />
    </svg>
  );
}

function PaceBlock({ view }: { view: HistoryView }) {
  const trend = trendLabel(view.pace.trend);
  return (
    <section className="history-pace" aria-live="polite">
      {view.pace.known && view.pace.daysBetweenUses !== undefined ? (
        <div>
          <p className="history-pace-figure">{formatPaceNumber(view.pace.daysBetweenUses)}</p>
          <p className="history-pace-label">days between uses</p>
        </div>
      ) : (
        <div>
          <p className="history-pace-unknown">Pace not known yet</p>
          <p className="history-pace-note">Two uses, at least a week apart, set the days between uses.</p>
        </div>
      )}
      <p className="history-support">{onHandLabel(view.onHand)}</p>
      {view.pace.daysLeftKnown && view.onHand > 0 && (
        <p className="history-support">{daysLeftLabel(view.pace.daysLeft ?? 0)}</p>
      )}
      {trend !== '' && <p className="history-pace-note">{trend}</p>}
      <Sparkline values={view.pace.sparkline ?? []} />
    </section>
  );
}

export function HistoryPage() {
  const { itemId, groupId } = useParams();
  const subjectId = itemId ?? groupId ?? '';
  const grouped = groupId !== undefined;
  const [view, setView] = useState<HistoryView | null>(null);
  const [filter, setFilter] = useState<Filter>('recent');
  const [loading, setLoading] = useState(true);
  const [error, setError] = useState('');
  const [editing, setEditing] = useState<HistoryMove | null>(null);
  const [quantity, setQuantity] = useState('1');
  const [undoing, setUndoing] = useState<HistoryMove | null>(null);
  const [pending, setPending] = useState(false);

  const load = useCallback(async () => {
    setError('');
    try {
      const next = grouped ? await getGroupHistory(subjectId) : await getItemHistory(subjectId);
      setView(next);
    } catch (requestError) {
      setError(requestError instanceof Error ? requestError.message : 'Unable to load history.');
    } finally {
      setLoading(false);
    }
  }, [grouped, subjectId]);

  useEffect(() => {
    void Promise.resolve().then(load);
  }, [load]);

  const shown = (view?.moves ?? []).filter((move) => filter === 'recent' || move.direction === filter);
  const now = new Date();

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
      await load();
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
      await load();
    } catch (requestError) {
      setError(requestError instanceof Error ? requestError.message : 'Unable to undo that move.');
    } finally {
      setPending(false);
    }
  };

  const undo = undoing ? undoCopy(undoing.direction, undoing.quantity) : null;

  return (
    <Stack className="bin-page history-page" gap="md">
      <Anchor className="history-back" component={Link} to={grouped ? `/groups/${subjectId}` : '/inventory'}>
        {grouped ? 'Group' : 'Inventory'}
      </Anchor>
      {loading && <Loader aria-label="Loading history" />}
      {error !== '' && editing === null && undoing === null && <Alert color="red">{error}</Alert>}
      {view && (
        <>
          <div className="history-heading">
            <h1 className="history-name">{view.name}</h1>
            {view.detail && <p className="history-detail">{view.detail}</p>}
          </div>
          <PaceBlock view={view} />
          <div className="history-filters" role="group" aria-label="Which moves to show">
            {filters.map((item) => (
              <button
                key={item.id}
                type="button"
                className="history-filter"
                aria-pressed={filter === item.id}
                onClick={() => setFilter(item.id)}
              >
                {item.label}
              </button>
            ))}
          </div>
          {view.moves.length === 0 && (
            <p className="history-empty">No stock moves yet. Stock this in or use some, and the pace shows up here.</p>
          )}
          {view.moves.length > 0 && shown.length === 0 && (
            <p className="history-empty">
              {filter === 'in' ? 'No stocked-in moves in this list.' : 'No uses in this list.'}
            </p>
          )}
          {shown.length > 0 && (
            <ol className="history-trail">
              {shown.map((move) => {
                const when = formatMoveWhen(new Date(move.at), now, move.source);
                const title = moveTitle(move.direction, move.quantity);
                return (
                  <li key={move.id} className="history-move">
                    <div className="history-move-copy">
                      <p className="history-move-title">{title}</p>
                      <p className="history-move-when">{when}</p>
                      {view.kind === 'group' && <p className="history-move-product">{move.productName}</p>}
                    </div>
                    <span
                      className={move.direction === 'in' ? 'history-delta history-delta-in' : 'history-delta history-delta-out'}
                      aria-label={deltaName(move.direction, move.quantity)}
                    >
                      {deltaText(move.direction, move.quantity)}
                    </span>
                    <Menu position="bottom-end" withinPortal withInitialFocusPlaceholder={false} transitionProps={{ duration: 0 }}>
                      <Menu.Target>
                        <ActionIcon variant="subtle" className="history-menu" aria-label={`Actions for ${title}, ${when}`}>
                          <KebabIcon />
                        </ActionIcon>
                      </Menu.Target>
                      <Menu.Dropdown>
                        {!quantityLocked(move, view.moves) && (
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
          )}
        </>
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
            <p className="history-empty">{undo.body}</p>
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
