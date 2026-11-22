import { useEffect, useRef, useState, type ReactNode } from 'react';
import { Link } from 'react-router-dom';
import { useMediaQuery } from '@mantine/hooks';
import { Alert, Badge, Button, Drawer, Group, Text } from '@mantine/core';
import { getProduct, listItemInstances } from '../../api/client';
import type { InventoryItem } from '@stockbot/shared/types';
import { ProvenanceBadge } from '../product/ProvenanceBadge';
import { ProductEditor } from '../product/ProductEditor';
import { TargetCard } from '../restock/TargetCard';
import type { RestockItem } from '../../restock/types';
import { ItemInstanceList } from './ItemInstanceList';
import { ProductPhoto } from './ProductPhoto';
import { visibleCategory } from './inventoryUtils';

const formatDate = (value: string): string =>
  new Intl.DateTimeFormat(undefined, { dateStyle: 'medium', timeZone: 'UTC' }).format(new Date(value));

function DetailRecords({
  item,
  children,
}: {
  item: InventoryItem;
  children: (barcodes: string[] | null, nextExpiry: string | null | undefined) => ReactNode;
}) {
  const [barcodes, setBarcodes] = useState<string[] | null>(null);
  const [nextExpiry, setNextExpiry] = useState<string | null | undefined>(undefined);

  useEffect(() => {
    let active = true;
    void getProduct(item.item.productId)
      .then((product) => {
        if (active) setBarcodes(product.barcodes ?? []);
      })
      .catch(() => {
        if (active) setBarcodes([]);
      });
    void listItemInstances(item.item.id)
      .then((instances) => {
        if (!active) return;
        const dated = instances
          .map((instance) => instance.expiresAt)
          .filter((value): value is string => value !== null)
          .sort();
        setNextExpiry(dated[0] ?? null);
      })
      .catch(() => {
        if (active) setNextExpiry(null);
      });
    return () => {
      active = false;
    };
  }, [item.item.id, item.item.productId]);

  return children(barcodes, nextExpiry);
}

export interface ItemDetailSheetProps {
  itemId: string | null;
  item: InventoryItem | null;
  hand: string;
  loading: boolean;
  loadError: string;
  offlineWithData: boolean;
  knownAt: string;
  groupName: string | null;
  groupId: string | null;
  restockItem: RestockItem | null;
  section: 'instances' | 'categories' | null;
  child: 'target' | 'group' | null;
  onClose: () => void;
  onEditTarget: () => void;
  onUseBroader?: () => void;
  onOpenCategory: (id: string) => void;
  onAddCategory?: () => void;
  onRemoveCategory: (id: string) => void;
  onAddToGroup: () => void;
  onHandChange: (delta: number) => void;
  onQuietSaved: () => void;
}

export function ItemDetailSheet({
  itemId,
  item,
  hand,
  loading,
  loadError,
  offlineWithData,
  knownAt,
  groupName,
  groupId,
  restockItem,
  section,
  child,
  onClose,
  onEditTarget,
  onUseBroader,
  onOpenCategory,
  onAddCategory,
  onRemoveCategory,
  onAddToGroup,
  onHandChange,
  onQuietSaved,
}: ItemDetailSheetProps) {
  const wide = useMediaQuery('(min-width: 48em)', false, { getInitialValueInEffect: false });
  const childWas = useRef<'target' | 'group' | null>(child);
  const titleRef = useRef<HTMLHeadingElement>(null);
  const goneRef = useRef<HTMLParagraphElement>(null);
  const opened = itemId !== null;
  const missing = opened && !loading && loadError === '' && item === null;

  useEffect(() => {
    const previous = childWas.current;
    childWas.current = child;
    if (!opened || child !== null) return undefined;
    const timer = window.setTimeout(() => {
      if (previous === 'target' || previous === 'group') {
        const id = previous === 'target' ? 'item-detail-target-opener' : 'item-detail-group-opener';
        const opener = document.getElementById(id);
        if (opener) {
          opener.focus();
          return;
        }
      }
      if (missing) goneRef.current?.focus();
      else titleRef.current?.focus();
    });
    return () => window.clearTimeout(timer);
  }, [opened, child, missing, item?.item.id]);

  useEffect(() => {
    if (!opened || !item || !section) return;
    const targetId = section === 'instances' ? `instances-${item.item.id}` : 'item-detail-categories';
    const frame = window.requestAnimationFrame(() => {
      document.getElementById(targetId)?.scrollIntoView({ block: 'start' });
    });
    return () => window.cancelAnimationFrame(frame);
  }, [opened, item, section]);

  const name = item?.item.product.name ?? '';
  const category = item ? visibleCategory(item.item.product.category) : null;

  return (
    <Drawer.Root
      opened={opened}
      onClose={onClose}
      position={wide ? 'right' : 'bottom'}
      size={wide ? '28rem' : '100%'}
      closeOnEscape={child === null}
      trapFocus={child === null}
      returnFocus={false}
      zIndex={90}
      transitionProps={{
        duration: 0,
        onEntered: () => {
          if (child !== null) return;
          if (missing) goneRef.current?.focus();
          else titleRef.current?.focus();
        },
      }}
    >
      <Drawer.Overlay />
      <Drawer.Content className="bin-page item-detail">
        <Drawer.Header component="div">
          {item ? (
            <Drawer.Title ref={titleRef} tabIndex={-1} data-autofocus className="item-detail-title">{name}</Drawer.Title>
          ) : missing ? (
            <Drawer.Title className="shelf-sr">Inventory</Drawer.Title>
          ) : (
            <Drawer.Title className="item-detail-title-skeleton">{' '}</Drawer.Title>
          )}
          <Drawer.CloseButton aria-label="Close" />
        </Drawer.Header>
        <Drawer.Body>
          {missing && (
            <p ref={goneRef} tabIndex={-1} data-autofocus role="status" className="item-detail-gone">
              This product is no longer in your inventory.
            </p>
          )}
          {!item && !missing && loadError !== '' && <Alert color="red">{loadError}</Alert>}
          {!item && !missing && loadError === '' && (
            <div className="item-detail-loading">
              <ProductPhoto name="" detail showSkeleton />
            </div>
          )}
          {item && (
            <DetailRecords key={item.item.id} item={item}>
            {(barcodes, nextExpiry) => (
            <div className="item-detail-body">
              {offlineWithData && (
                <p className="item-detail-offline" role="status">
                  {`You're offline. This is what we knew at ${knownAt}.`}
                </p>
              )}
              <ProductPhoto src={item.item.product.imageUrl} name={name} detail showSkeleton />
              <div className="item-detail-facts">
                {category !== null && <p className="shelf-category">{category}</p>}
                <p className="item-detail-hand">{hand}</p>
                <div className="shelf-meta">
                  {item.nearExpiryCount > 0 && <Badge size="sm" color="yellow">{item.nearExpiryCount} near expiry</Badge>}
                  {item.expiredCount > 0 && <Badge size="sm" color="red">{item.expiredCount} expired</Badge>}
                </div>
                <p className="item-detail-expiry">
                  {nextExpiry === undefined ? <span className="item-detail-line-skeleton" aria-hidden="true" /> : nextExpiry === null ? 'No expiration date' : `Expires ${formatDate(nextExpiry)}`}
                </p>
                <ProvenanceBadge quiet externalSource={item.item.product.externalSource} />
                <p className="item-detail-barcode">
                  {barcodes === null ? (
                    <span className="item-detail-line-skeleton" aria-hidden="true" />
                  ) : barcodes.length > 0 ? (
                    <Text component="span" size="xs" c="dimmed" className="copyable-barcode">Barcode: {barcodes.join(', ')}</Text>
                  ) : null}
                </p>
              </div>
              <div className="item-detail-group-slot">
                {groupName && groupId && (
                  <>
                    <p>{`This product is part of the ${groupName} group.`}</p>
                    <Link className="shelf-settings" to={`/groups/${groupId}`}>Open group settings ›</Link>
                  </>
                )}
              </div>
              {(loading || restockItem) && (
                <div className="item-detail-target-slot">
                  {restockItem ? (
                    <TargetCard
                      item={restockItem}
                      editButtonId="item-detail-target-opener"
                      onEdit={onEditTarget}
                      onUseBroader={onUseBroader}
                      onOpen={restockItem.level === 'category' && restockItem.categories[0]
                        ? () => onOpenCategory(restockItem.categories.find((entry) => entry.name === restockItem.levelName)?.id ?? restockItem.categories[0].id)
                        : undefined}
                    />
                  ) : (
                    <div className="item-detail-block-skeleton" aria-hidden="true" />
                  )}
                </div>
              )}
              <div id="item-detail-categories" className="item-detail-categories">
                <Group gap={6}>
                  {restockItem?.categories.map((entry) => (
                    <Button key={entry.id} variant="default" radius="xl" onClick={() => onOpenCategory(entry.id)}>
                      {entry.name}
                    </Button>
                  ))}
                  {onAddCategory && (
                    <Button variant="default" radius="xl" onClick={onAddCategory}>+ Category</Button>
                  )}
                  {restockItem?.categories.map((entry) => (
                    <Button key={`remove-${entry.id}`} variant="subtle" onClick={() => onRemoveCategory(entry.id)}>
                      {`Remove ${entry.name}`}
                    </Button>
                  ))}
                </Group>
              </div>
              <div className="item-detail-actions">
                <Button id="item-detail-group-opener" variant="default" onClick={onAddToGroup}>Add to a group…</Button>
                <Button component={Link} to={`/inventory/${item.item.id}/history`} variant="default">History</Button>
              </div>
              <div className="item-detail-instances">
                <ItemInstanceList
                  itemId={item.item.id}
                  productName={name}
                  onHand={item.instanceCount}
                  onHandChange={onHandChange}
                  onInventoryChanged={onQuietSaved}
                />
              </div>
              <ProductEditor
                productId={item.item.productId}
                group={groupId && groupName ? { id: groupId, name: groupName } : null}
                onSaved={onQuietSaved}
              />
              {loadError !== '' && <Alert color="red">{loadError}</Alert>}
            </div>
            )}
            </DetailRecords>
          )}
        </Drawer.Body>
      </Drawer.Content>
    </Drawer.Root>
  );
}
