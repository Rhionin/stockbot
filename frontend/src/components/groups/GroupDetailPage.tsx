import { useCallback, useEffect, useState, type CSSProperties } from 'react';
import { Link, useNavigate, useParams } from 'react-router-dom';
import {
  ActionIcon, Alert, Anchor, Button, Group, Loader, Menu, Modal, Stack, Text, TextInput, Tooltip,
} from '@mantine/core';
import {
  addGroupMembers, deleteGroup, getGroup, getInventoryList, getProduct, getSupplySettings, listGroups, listProducts, previewGroupTarget, removeGroupMember, renameGroup, setGroupTarget, setMemberRestock,
} from '../../api/client';
import type { GroupTarget, ProductDetail, ProductGroup } from '@stockbot/shared/types';
import { AddToGroup } from './AddToGroup';
import { BinMark } from './BinMark';
import { ungroupedProducts, type AddCandidate } from './candidates';
import {
  binColor, binView, memberLine, memberOrder, memberPackage, onHandOunces, rulePillLabel, ruleSentence, type MemberPackage,
} from './copy';
import { RulePicker } from './RulePicker';
import { restockNote } from './restock';
import { StopDialog } from '../restock/StopDialog';
import { restockCopy } from '../../restock/copy';
import { Seesaw } from './Seesaw';
import type { RestockPreview } from '../../restock/types';
import { seesawView, stockMeter, type SeesawDraft } from './seesaw';
import { groupStock } from '../../stockState';
import { inlineFailure } from '@stockbot/shared/contract';
import { showUndo } from '../restock/undo';
import '../history/history.css';

const closeButton = { 'aria-label': 'Close' };

const iconInk = {
  '--ai-color': 'var(--bin-ink)',
  '--ai-hover-color': 'var(--bin-ink)',
  color: 'var(--bin-ink)',
} as CSSProperties;

function KebabIcon() {
  return (
    <svg viewBox="0 0 16 16" width="16" height="16" aria-hidden="true">
      <circle cx="8" cy="3.1" r="1.35" fill="currentColor" />
      <circle cx="8" cy="8" r="1.35" fill="currentColor" />
      <circle cx="8" cy="12.9" r="1.35" fill="currentColor" />
    </svg>
  );
}

function MemberRows({
  members,
  palette,
  others,
  moving,
  onRemove,
  onMove,
  onRestock,
}: {
  members: MemberPackage[];
  palette: MemberPackage[];
  others: ProductGroup[];
  moving: string;
  onRemove: (productId: string) => void;
  onMove: (productId: string, destination: string) => void;
  onRestock: (productId: string, noRestock: boolean) => void;
}) {
  return members.map((member) => (
    <article key={member.productId} className={member.noRestock ? 'bin-member is-aside' : 'bin-member'}>
      <span className="bin-swatch" style={{ background: binColor(palette, member.productId) }} aria-hidden="true" />
      <div className="bin-member-copy">
        <p className="bin-member-name">{member.name}</p>
        {member.noRestock && <span className="bin-restock-tag">⊘ no restock</span>}
        <Text size="sm" c="dimmed">{memberLine(member)}</Text>
        {(member.barcodes ?? []).length > 0 && (
          <Text size="xs" c="dimmed">Barcode: {(member.barcodes ?? []).join(', ')}</Text>
        )}
      </div>
      <Menu position="bottom-end" withInitialFocusPlaceholder={false} transitionProps={{ duration: 0 }}>
        <Menu.Target>
          <Tooltip label="Remove or move">
            <ActionIcon
              variant="subtle"
              color="dark"
              className="bin-icon-button"
              style={iconInk}
              aria-label={`Remove or move ${member.name}`}
            >
              <KebabIcon />
            </ActionIcon>
          </Tooltip>
        </Menu.Target>
        <Menu.Dropdown className="bin-member-menu">
          {member.noRestock ? (
            <Menu.Item onClick={() => onRestock(member.productId, false)}>Keep in rotation</Menu.Item>
          ) : (
            <Menu.Item onClick={() => onRestock(member.productId, true)}>
              <span className="bin-menu-label">Don't restock</span>
              <span className="bin-menu-hint">Stays in the group and counts toward stock</span>
            </Menu.Item>
          )}
          <Menu.Item onClick={() => onRemove(member.productId)}>Remove</Menu.Item>
          {others.length > 0 && <Menu.Label>Move to</Menu.Label>}
          {others.map((item) => (
            <Menu.Item
              key={item.id}
              disabled={moving === member.productId}
              onClick={() => onMove(member.productId, item.id)}
            >
              {item.name}
            </Menu.Item>
          ))}
        </Menu.Dropdown>
      </Menu>
    </article>
  ));
}

export const GroupDetailPage = () => {
  const { id = '' } = useParams();
  const navigate = useNavigate();
  const [group, setGroup] = useState<ProductGroup | null>(null);
  const [others, setOthers] = useState<ProductGroup[]>([]);
  const [details, setDetails] = useState<Record<string, Partial<ProductDetail>>>({});
  const [householdMonths, setHouseholdMonths] = useState<number | undefined>();
  const [name, setName] = useState('');
  const [renaming, setRenaming] = useState(false);
  const [moving, setMoving] = useState('');
  const [draft, setDraft] = useState<SeesawDraft | null>(null);
  const [groupPreview, setGroupPreview] = useState<RestockPreview | null>(null);
  const [adding, setAdding] = useState(false);
  const [candidates, setCandidates] = useState<AddCandidate[]>([]);
  const [ruleOpen, setRuleOpen] = useState(false);
  const [loading, setLoading] = useState(true);
  const [error, setError] = useState('');
  const [deleting, setDeleting] = useState(false);
  const [deleteLoading, setDeleteLoading] = useState(false);
  const [deleteError, setDeleteError] = useState('');

  const load = useCallback(async () => {
    setError('');
    try {
      const [view, all, products, inventory, settings] = await Promise.all([
        getGroup(id),
        listGroups(),
        listProducts(),
        getInventoryList(),
        getSupplySettings().catch(() => null),
      ]);
      setGroup(view);
      setName(view.name);
      setOthers(all.filter((item) => item.id !== view.id));
      setHouseholdMonths(settings?.months);
      setCandidates(ungroupedProducts(products, inventory, [view, ...all]));
      const loaded: Record<string, Partial<ProductDetail>> = {};
      await Promise.all(view.members.map(async (member) => {
        try {
          loaded[member.productId] = await getProduct(member.productId);
        } catch {
          loaded[member.productId] = { barcodes: [] };
        }
      }));
      setDetails(loaded);
    } catch (requestError) {
      setError(inlineFailure(requestError, 'Unable to load that group.') ?? '');
    } finally {
      setLoading(false);
    }
  }, [id]);

  useEffect(() => {
    void Promise.resolve().then(load);
  }, [load]);

  const rename = async () => {
    if (!group || name.trim() === '') return;
    setError('');
    try {
      setGroup(await renameGroup(group.id, name.trim()));
      setRenaming(false);
    } catch (requestError) {
      setError(inlineFailure(requestError, 'Unable to rename the group.') ?? '');
    }
  };

  const remove = async (productId: string) => {
    if (!group) return;
    setError('');
    try {
      const result = await removeGroupMember(group.id, productId);
      if (result.deleted) {
        navigate('/inventory?filter=groups');
        return;
      }
      await load();
    } catch (requestError) {
      setError(inlineFailure(requestError, 'Unable to remove that product.') ?? '');
    }
  };

  const move = async (productId: string, destination: string) => {
    setMoving(productId);
    setError('');
    try {
      await addGroupMembers(destination, [productId], id);
      await load();
    } catch (requestError) {
      setError(inlineFailure(requestError, 'Unable to move that product.') ?? '');
    } finally {
      setMoving('');
    }
  };

  const saveTarget = async (body: GroupTarget) => {
    if (!group) return;
    setGroup(await setGroupTarget(group.id, body));
  };

  const restock = async (productId: string, noRestock: boolean) => {
    if (!group) return;
    setError('');
    try {
      await setMemberRestock(group.id, productId, noRestock);
      await load();
    } catch (requestError) {
      setError(inlineFailure(requestError, 'Unable to update that product.') ?? '');
    }
  };

  const confirmDelete = async () => {
    if (!group) return;
    setDeleteLoading(true);
    setDeleteError('');
    try {
      await deleteGroup(group.id);
      setDeleteLoading(false);
      setDeleting(false);
      showUndo({ detail: `Deleted ${group.name}. Items stay in inventory.` });
      navigate('/inventory?filter=groups');
    } catch (err) {
      setDeleteError(inlineFailure(err, 'Unable to delete the group.') ?? 'Unable to delete the group.');
      setDeleteLoading(false);
    }
  };

  if (loading) return <Loader aria-label="Loading group" />;

  const members: MemberPackage[] = memberOrder((group?.members ?? []).map((member) => (
    memberPackage(member, details[member.productId])
  )));
  const shown = group ? seesawView(group, members, householdMonths, draft) : null;
  const view = group && shown
    ? binView(
      shown.fillQuantity !== undefined
        ? { quantity: shown.fillQuantity, dimension: shown.dimension }
        : { windowMonths: shown.time.pinned ? group.windowMonths : undefined },
      members,
    )
    : null;
  const ruleName = group ? rulePillLabel(group, members) : '';
  const ruleDetail = group ? ruleSentence(group, members, 'detail') : '';
  const note = group ? restockNote(group, members) : '';
  const rotating = members.filter((member) => !member.noRestock);
  const aside = members.filter((member) => member.noRestock);
  const meter = shown ? stockMeter(shown, onHandOunces(members, shown.dimension)) : null;
  const unitsOnHand = members.reduce((sum, member) => sum + member.onHand, 0);
  const stock = group ? groupStock(unitsOnHand, group.runningLow, members.length) : undefined;

  return (
    <Stack gap="lg" className="bin-page">
      <Group justify="space-between">
        <Group gap="md">
          <Anchor className="history-back" component={Link} to="/inventory?filter=groups">Inventory</Anchor>
          {group && (
            <Anchor className="history-back" component={Link} to={`/groups/${group.id}/history`}>History</Anchor>
          )}
        </Group>
        {group && (
          <Menu position="bottom-end" withInitialFocusPlaceholder={false} transitionProps={{ duration: 0 }}>
            <Menu.Target>
              <ActionIcon
                variant="subtle"
                color="dark"
                className="bin-icon-button"
                style={iconInk}
                aria-label={`Actions for ${group.name}`}
              >
                <KebabIcon />
              </ActionIcon>
            </Menu.Target>
            <Menu.Dropdown className="bin-member-menu">
              <Menu.Item
                onClick={() => {
                  void previewGroupTarget(group.id, { kind: 'months', value: 0 }).then(setGroupPreview);
                }}
              >
                <span className="bin-menu-label">Don't restock…</span>
                <span className="bin-menu-hint">{restockCopy.previewHint(group.members.length)}</span>
              </Menu.Item>
              <Menu.Divider />
              <Menu.Item
                color="red"
                onClick={() => {
                  setDeleting(true);
                  setDeleteError('');
                }}
              >
                Delete group…
              </Menu.Item>
            </Menu.Dropdown>
          </Menu>
        )}
      </Group>
      {group && (
        <Modal
          opened={deleting}
          onClose={() => {
            if (!deleteLoading) {
              setDeleting(false);
              setDeleteError('');
            }
          }}
          title={`Delete ${group.name}?`}
          closeButtonProps={closeButton}
        >
          <Stack gap="sm">
            {group.members.length > 0 ? (
              <Text size="sm">
                Your {group.members.length === 1 ? '1 product stays' : `${group.members.length} products stay`} in inventory with their current stock on hand. They will no longer share a group target or rotation rule.
              </Text>
            ) : (
              <Text size="sm">This empty group will be removed. Your inventory won't change.</Text>
            )}
            {deleteError !== '' && <Alert color="red" role="alert" py="xs">{deleteError}</Alert>}
            <Group justify="flex-end" gap="sm" mt="sm">
              <Button
                variant="default"
                disabled={deleteLoading}
                onClick={() => {
                  setDeleting(false);
                  setDeleteError('');
                }}
              >
                Cancel
              </Button>
              <Button color="red" loading={deleteLoading} onClick={() => void confirmDelete()}>
                Delete group
              </Button>
            </Group>
          </Stack>
        </Modal>
      )}
      {error !== '' && !renaming && <Alert color="red" py="xs">{error}</Alert>}
      {group && view && shown && (
        <>
          <div className="bin-hero-row">
            <BinMark
              view={view}
              members={members}
              size="hero"
              name={group.name}
              caption={shown.caption}
              meter={meter ?? undefined}
              stock={stock}
              onRename={() => {
                setName(group.name);
                setError('');
                setRenaming(true);
              }}
            />
            <div className="seesaw-slot">
              <Seesaw
                view={shown}
                group={group}
                draft={draft}
                onDraft={setDraft}
                onSave={saveTarget}
              />
            </div>
            <div className="bin-copy">
              <div className="rule-block">
                <Tooltip label={ruleDetail}>
                  <button
                    type="button"
                    className="rule-pill"
                    aria-label={group.ruleConfirmed ? `Change rule, ${ruleName}` : ruleName}
                    onClick={() => setRuleOpen(true)}
                  >
                    <span className="rule-pill-mark" aria-hidden="true">⇄</span>
                    <span className="rule-pill-label">{ruleName}</span>
                  </button>
                </Tooltip>
                {note !== '' && <p className="rule-next">{note}</p>}
              </div>
            </div>
          </div>
          <div className="bin-members">
            {aside.length > 0 ? (
              <>
                <section className="bin-section" aria-label="In rotation">
                  <h2 className="bin-section-title">In rotation</h2>
                  {rotating.length === 0 && <p className="bin-section-empty">Nothing is in rotation.</p>}
                  <MemberRows
                    members={rotating}
                    palette={members}
                    others={others}
                    moving={moving}
                    onRemove={(productId) => void remove(productId)}
                    onMove={(productId, destination) => void move(productId, destination)}
                    onRestock={(productId, noRestock) => void restock(productId, noRestock)}
                  />
                </section>
                <section className="bin-section" aria-label="Not restocked">
                  <h2 className="bin-section-title">Not restocked</h2>
                  <MemberRows
                    members={aside}
                    palette={members}
                    others={others}
                    moving={moving}
                    onRemove={(productId) => void remove(productId)}
                    onMove={(productId, destination) => void move(productId, destination)}
                    onRestock={(productId, noRestock) => void restock(productId, noRestock)}
                  />
                </section>
              </>
            ) : (
              <>
                <MemberRows
                  members={members}
                  palette={members}
                  others={others}
                  moving={moving}
                  onRemove={(productId) => void remove(productId)}
                  onMove={(productId, destination) => void move(productId, destination)}
                  onRestock={(productId, noRestock) => void restock(productId, noRestock)}
                />
                {members.length === 0 && <Text c="dimmed">This group has no products yet.</Text>}
              </>
            )}
          </div>
          <Button color="dark" w="fit-content" onClick={() => setAdding(true)}>Add a product</Button>
          <Modal
            opened={renaming}
            onClose={() => setRenaming(false)}
            title="Rename"
            closeButtonProps={closeButton}
          >
            <Stack gap="sm">
              <TextInput label="Name" value={name} onChange={(event) => setName(event.currentTarget.value)} />
              {error !== '' && <Text size="sm" c="red">{error}</Text>}
              <Button onClick={() => void rename()} disabled={name.trim() === ''}>Rename</Button>
            </Stack>
          </Modal>
          <Modal
            opened={adding}
            onClose={() => setAdding(false)}
            title="Add a product"
            closeButtonProps={closeButton}
          >
            {adding && (
              <AddToGroup
                groupId={group.id}
                group={group}
                candidates={candidates}
                showHeading={false}
                onAdded={() => {
                  setAdding(false);
                  void load();
                }}
              />
            )}
          </Modal>
          {ruleOpen && (
            <RulePicker
              group={group}
              opened
              onClose={() => setRuleOpen(false)}
              onSaved={() => { void load(); }}
            />
          )}
        </>
      )}
      <StopDialog
        opened={groupPreview !== null}
        preview={groupPreview}
        onClose={() => setGroupPreview(null)}
        onConfirm={() => {
          if (!group) return;
          void setGroupTarget(group.id, { windowMonths: 0 }).then(() => {
            setGroupPreview(null);
            void load();
          });
        }}
      />
    </Stack>
  );
};
