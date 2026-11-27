import { useState } from 'react';
import { Link } from 'react-router-dom';
import { ActionIcon, Badge, Menu, Text } from '@mantine/core';
import { removeGroupMember, setMemberRestock } from '../../api/client';
import { memberLine } from '../groups/copy';
import { ProductPhoto } from './ProductPhoto';
import type { ShelfGroupRow, ShelfMember } from './shelf';
import { groupStock } from '../../stockState';
import { inlineFailure } from '@stockbot/shared/contract';

export interface GroupRowProps {
  row: ShelfGroupRow;
  expanded: boolean;
  onToggle: () => void;
  onMove: (member: ShelfMember) => void;
  onOpenMember: (member: ShelfMember) => void;
  onChanged: () => void;
  highlightedItemId?: string | null;
}

function PinIcon() {
  return (
    <svg className="shelf-pin-icon" viewBox="0 0 24 24" aria-hidden="true">
      <path fill="currentColor" d="M16 12V4h1V2H7v2h1v8l-2 2v2h5.2v6h1.6v-6H18v-2l-2-2z" />
    </svg>
  );
}

function Chevron({ expanded }: { expanded: boolean }) {
  return (
    <svg className={expanded ? 'shelf-chevron is-open' : 'shelf-chevron'} viewBox="0 0 24 24" aria-hidden="true">
      <path fill="currentColor" d="M7.4 9.4 12 14l4.6-4.6L18 10.8l-6 6-6-6z" />
    </svg>
  );
}

function KebabIcon() {
  return (
    <svg viewBox="0 0 16 16" width="16" height="16" aria-hidden="true">
      <circle cx="8" cy="3.1" r="1.35" fill="currentColor" />
      <circle cx="8" cy="8" r="1.35" fill="currentColor" />
      <circle cx="8" cy="12.9" r="1.35" fill="currentColor" />
    </svg>
  );
}

export function GroupRow({ row, expanded, onToggle, onMove, onOpenMember, onChanged, highlightedItemId = null }: GroupRowProps) {
  const [error, setError] = useState('');
  const panelId = `shelf-group-${row.id}`;
  const fill = row.meter.max > 0 ? Math.min(100, (row.meter.now / row.meter.max) * 100) : 0;
  const onHand = row.members.reduce((sum, member) => sum + member.onHand, 0);
  const stock = groupStock(onHand, row.group.runningLow, row.members.length);

  const restock = async (member: ShelfMember, noRestock: boolean) => {
    setError('');
    try {
      await setMemberRestock(row.id, member.productId, noRestock);
      onChanged();
    } catch (requestError) {
      setError(inlineFailure(requestError, 'Unable to update that product.') ?? '');
    }
  };

  const remove = async (member: ShelfMember) => {
    setError('');
    try {
      await removeGroupMember(row.id, member.productId);
      onChanged();
    } catch (requestError) {
      setError(inlineFailure(requestError, 'Unable to remove that product.') ?? '');
    }
  };

  return (
    <article className={expanded ? 'shelf-row is-expanded' : 'shelf-row'} id={`shelf-row-${row.id}`} data-stock={stock}>
      <div className="shelf-head">
        <ProductPhoto src={row.photoSrc} name={row.photoName} stacked />
        <div className="shelf-copy">
          <div className="shelf-title">
            <button
              type="button"
              className="shelf-open"
              aria-expanded={expanded}
              aria-controls={panelId}
              onClick={onToggle}
            >
              <h2 className="shelf-name">{row.group.name}</h2>
            </button>
            <span className="shelf-count shelf-raise">{row.group.members.length}</span>
          </div>
          <p className="shelf-meta">
            <span>{row.status}</span>
            {row.rulePill && <span className="shelf-pill">{row.rulePill}</span>}
          </p>
          <div
            className="shelf-meter"
            role="meter"
            aria-label="Stock on hand"
            aria-valuemin={0}
            aria-valuemax={row.meter.max}
            aria-valuenow={row.meter.now}
            aria-valuetext={row.meter.text}
          >
            <span className="shelf-meter-fill" style={{ width: `${fill}%` }} />
          </div>
          <p className="shelf-pin">
            <PinIcon />
            <span>{row.pinLabel}</span>
          </p>
          <Link className="shelf-history shelf-raise" to={`/groups/${row.id}/history`} onPointerDown={(event) => event.stopPropagation()}>History</Link>
        </div>
        <button
          type="button"
          className="shelf-expand shelf-raise"
          aria-expanded={expanded}
          aria-controls={panelId}
          aria-label={`${expanded ? 'Collapse' : 'Expand'} ${row.group.name}`}
          onClick={onToggle}
        >
          <Chevron expanded={expanded} />
        </button>
      </div>
      {expanded && (
        <div id={panelId} className="shelf-members">
          {row.members.map((member) => (
            <div
              key={member.productId}
              className={[
                'shelf-member',
                member.itemId ? 'is-openable' : '',
                member.itemId && highlightedItemId === member.itemId ? 'is-return' : '',
              ].filter(Boolean).join(' ')}
              id={member.itemId ? `shelf-row-${member.itemId}` : undefined}
            >
              <ProductPhoto src={member.imageUrl} name={member.name} />
              <div className="shelf-member-copy">
                {member.itemId ? (
                  <button
                    type="button"
                    id={`shelf-open-${member.itemId}`}
                    className="shelf-open"
                    aria-label={`Details for ${member.name}`}
                    aria-haspopup="dialog"
                    onClick={() => onOpenMember(member)}
                  >
                    <p className="shelf-member-name">{member.name}</p>
                  </button>
                ) : (
                  <p className="shelf-member-name">{member.name}</p>
                )}
                <Text size="sm" c="dimmed">{memberLine(member)}</Text>
                {member.noRestock && <span className="shelf-aside">⊘ no restock</span>}
                {member.nearExpiryCount > 0 && <Badge size="sm" color="yellow">{member.nearExpiryCount} near expiry</Badge>}
                {member.expiredCount > 0 && <Badge size="sm" color="red">{member.expiredCount} expired</Badge>}
                {(member.barcodes ?? []).length > 0 && (
                  <Text size="xs" c="dimmed" className="copyable-barcode">Barcode: {(member.barcodes ?? []).join(', ')}</Text>
                )}
              </div>
              <Menu position="bottom-end" withinPortal withInitialFocusPlaceholder={false} transitionProps={{ duration: 0 }}>
                <Menu.Target>
                  <ActionIcon variant="subtle" className="shelf-menu shelf-raise" aria-label={`Actions for ${member.name}`}>
                    <KebabIcon />
                  </ActionIcon>
                </Menu.Target>
                <Menu.Dropdown className="bin-member-menu">
                  {member.noRestock ? (
                    <Menu.Item onClick={() => void restock(member, false)}>Keep in rotation</Menu.Item>
                  ) : (
                    <Menu.Item onClick={() => void restock(member, true)}>Don't restock</Menu.Item>
                  )}
                  <Menu.Item onClick={() => onMove(member)}>Move to another group</Menu.Item>
                  <Menu.Item onClick={() => void remove(member)}>Remove from group</Menu.Item>
                  {member.itemId && (
                    <>
                      <Menu.Item component={Link} to={`/inventory/${member.itemId}/history`}>
                        View history
                      </Menu.Item>
                      <Menu.Item onClick={() => onOpenMember(member)}>View details</Menu.Item>
                    </>
                  )}
                </Menu.Dropdown>
              </Menu>
            </div>
          ))}
          {error !== '' && <Text size="sm" c="red">{error}</Text>}
          <Link className="shelf-settings" to={`/groups/${row.id}`}>Open group settings ›</Link>
        </div>
      )}
    </article>
  );
}
