import { useState } from 'react';
import { Link } from 'react-router-dom';
import { ActionIcon, Badge, Menu, Text } from '@mantine/core';
import { removeGroupMember, setMemberRestock } from '../../api/client';
import { ProductEditor } from '../product/ProductEditor';
import { memberLine } from '../groups/copy';
import { ItemInstanceList } from './ItemInstanceList';
import { ProductPhoto } from './ProductPhoto';
import type { ShelfGroupRow, ShelfMember } from './shelf';
import { groupStock } from '../../stockState';
import { inlineFailure } from '../../api/contract';

export interface GroupRowProps {
  row: ShelfGroupRow;
  expanded: boolean;
  onToggle: () => void;
  onMove: (member: ShelfMember) => void;
  onChanged: () => void;
  onHandChange: (itemId: string, delta: number) => void;
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

export function GroupRow({ row, expanded, onToggle, onMove, onChanged, onHandChange }: GroupRowProps) {
  const [instancesFor, setInstancesFor] = useState<string | null>(null);
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
    <article className="shelf-row" data-stock={stock}>
      <div className="shelf-head">
        <ProductPhoto src={row.photoSrc} name={row.photoName} stacked />
        <div className="shelf-copy">
          <div className="shelf-title">
            <h2 className="shelf-name">{row.group.name}</h2>
            <span className="shelf-count">{row.group.members.length}</span>
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
          <Link className="shelf-history" to={`/groups/${row.id}/history`} onPointerDown={(event) => event.stopPropagation()}>History</Link>
        </div>
        <button
          type="button"
          className="shelf-expand"
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
            <div key={member.productId} className="shelf-member">
              <ProductPhoto src={member.imageUrl} name={member.name} />
              <div className="shelf-member-copy">
                <p className="shelf-member-name">{member.name}</p>
                <Text size="sm" c="dimmed">{memberLine(member)}</Text>
                {member.noRestock && <span className="shelf-aside">⊘ no restock</span>}
                {member.nearExpiryCount > 0 && <Badge size="sm" color="yellow">{member.nearExpiryCount} near expiry</Badge>}
                {member.expiredCount > 0 && <Badge size="sm" color="red">{member.expiredCount} expired</Badge>}
                {(member.barcodes ?? []).length > 0 && (
                  <Text size="xs" c="dimmed" className="copyable-barcode">Barcode: {(member.barcodes ?? []).join(', ')}</Text>
                )}
                {instancesFor === member.productId && member.itemId && (
                  <>
                    <ItemInstanceList
                      itemId={member.itemId}
                      productName={member.name}
                      onHand={member.onHand}
                      onHandChange={(delta) => onHandChange(member.itemId ?? '', delta)}
                      onInventoryChanged={onChanged}
                    />
                    <ProductEditor
                      productId={member.productId}
                      group={{ id: row.id, name: row.group.name }}
                      onSaved={onChanged}
                    />
                  </>
                )}
              </div>
              <Menu position="bottom-end" withinPortal withInitialFocusPlaceholder={false} transitionProps={{ duration: 0 }}>
                <Menu.Target>
                  <ActionIcon variant="subtle" className="shelf-menu" aria-label={`Actions for ${member.name}`}>
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
                      <Menu.Item onClick={() => setInstancesFor((current) => current === member.productId ? null : member.productId)}>
                        {instancesFor === member.productId ? 'Hide instances' : 'View instances'}
                      </Menu.Item>
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
