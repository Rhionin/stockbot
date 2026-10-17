import { useCallback, useEffect, useState } from 'react';
import { Link, useNavigate, useParams } from 'react-router-dom';
import { ActionIcon, Alert, Anchor, Button, Group, Loader, Menu, Modal, Stack, Text, TextInput, Title } from '@mantine/core';
import {
  deleteCategory, getRestock, mergeCategory, previewCategoryTarget, renameCategory, setCategoryTarget,
} from '../../api/client';
import type { CategorySummary, RestockItem, RestockPreview, RestockTarget } from '../../restock/types';
import { restockCopy } from '../../restock/copy';
import { StopDialog } from './StopDialog';
import { TargetEditor } from './TargetEditor';
import './restock.css';

function Kebab() {
  return (
    <svg viewBox="0 0 16 16" width="16" height="16" aria-hidden="true">
      <circle cx="8" cy="3.1" r="1.35" fill="currentColor" />
      <circle cx="8" cy="8" r="1.35" fill="currentColor" />
      <circle cx="8" cy="12.9" r="1.35" fill="currentColor" />
    </svg>
  );
}

export function CategoryPage() {
  const { id = '' } = useParams();
  const navigate = useNavigate();
  const [category, setCategory] = useState<CategorySummary | null>(null);
  const [items, setItems] = useState<RestockItem[]>([]);
  const [loading, setLoading] = useState(true);
  const [error, setError] = useState('');
  const [editor, setEditor] = useState(false);
  const [preview, setPreview] = useState<RestockPreview | null>(null);
  const [pending, setPending] = useState<RestockTarget | null>(null);
  const [rename, setRename] = useState('');
  const [renaming, setRenaming] = useState(false);
  const [mergeOpen, setMergeOpen] = useState(false);
  const [others, setOthers] = useState<CategorySummary[]>([]);

  const load = useCallback(async () => {
    setLoading(true);
    setError('');
    try {
      const snap = await getRestock();
      const found = snap?.categories.find((row) => row.id === id) ?? null;
      setCategory(found);
      setOthers((snap?.categories ?? []).filter((row) => row.id !== id));
      setItems((snap?.items ?? []).filter((item) => item.categories.some((member) => member.id === id)));
      if (!found) setError("That category isn't in the household.");
    } catch (requestError) {
      setError(requestError instanceof Error ? requestError.message : 'Unable to load the category.');
    } finally {
      setLoading(false);
    }
  }, [id]);

  useEffect(() => {
    void Promise.resolve().then(load);
  }, [load]);

  const apply = async (target: RestockTarget) => {
    if (target.value === 0) {
      const next = await previewCategoryTarget(id, target);
      setPending(target);
      setPreview(next);
      return;
    }
    await setCategoryTarget(id, target);
    await load();
  };

  const confirm = async () => {
    if (!pending) return;
    await setCategoryTarget(id, pending);
    setPreview(null);
    setPending(null);
    await load();
  };

  if (loading) return <Loader aria-label="Loading category" />;

  const setLabel = category?.target ? 'Change' : `Set for ${category?.name ?? 'category'}`;

  return (
    <Stack gap="sm" className="shelf-page">
      <Anchor component={Link} to="/inventory?filter=categories" className="history-back">Inventory</Anchor>
      {error !== '' && <Alert color="red">{error}</Alert>}
      {category && (
        <>
          <Group justify="space-between" align="flex-start">
            <div>
              <Title order={1} size="h3">{category.name}</Title>
              <Text c="dimmed" size="sm">{`Household category · ${category.items} items`}</Text>
            </div>
            <Menu position="bottom-end">
              <Menu.Target>
                <ActionIcon variant="subtle" aria-label={`Actions for ${category.name}`}><Kebab /></ActionIcon>
              </Menu.Target>
              <Menu.Dropdown>
                <Menu.Item onClick={() => setEditor(true)}>Restock target…</Menu.Item>
                <Menu.Item onClick={() => void apply({ kind: 'months', value: 0 })}>
                  Don't restock…
                  <Text size="xs" c="dimmed">{restockCopy.previewHint(category.items)}</Text>
                </Menu.Item>
                <Menu.Divider />
                <Menu.Item onClick={() => { setRename(category.name); setRenaming(true); }}>Rename</Menu.Item>
                <Menu.Item onClick={() => setMergeOpen(true)}>Merge into…</Menu.Item>
                <Menu.Item color="red" onClick={() => {
                  if (window.confirm(`Delete ${category.name}? Items stay in inventory.`)) {
                    void deleteCategory(id).then(() => navigate('/inventory?filter=categories'));
                  }
                }}
                >
                  Delete category…
                </Menu.Item>
              </Menu.Dropdown>
            </Menu>
          </Group>
          <div className="restock-target">
            <div className="restock-kicker">Restock target</div>
            <div className="restock-value">
              {category.valueLabel}
              {category.target?.kind === 'months' && (category.target.value ?? 0) > 0 && <span className="restock-unit"> of supply</span>}
            </div>
            <div className="restock-source">
              <span className={category.target ? 'restock-dot is-solid' : 'restock-dot'} />
              <b>{category.sourceLabel}</b>
            </div>
            <div className="restock-actions">
              <Button size="xs" variant="light" onClick={() => setEditor(true)}>{setLabel}</Button>
            </div>
          </div>
          <Text size="sm" c="dimmed">Items get this target unless they, or their group, set their own.</Text>
          <div className="shelf-list">
            {items.map((item) => (
              <article key={item.itemId} className="shelf-row">
                <div className="shelf-head">
                  <div className="shelf-copy">
                    <h2 className="shelf-name">{item.name}</h2>
                    <p className="shelf-category">{item.rowSource}</p>
                  </div>
                </div>
              </article>
            ))}
          </div>
          <TargetEditor
            opened={editor}
            title={`Restock target for ${category.name}`}
            initial={category.target}
            onClose={() => setEditor(false)}
            onSave={apply}
          />
          <StopDialog
            opened={preview !== null}
            preview={preview}
            onClose={() => { setPreview(null); setPending(null); }}
            onConfirm={() => void confirm()}
          />
          <Modal opened={renaming} onClose={() => setRenaming(false)} title="Rename">
            <Stack>
              <TextInput label="Name" value={rename} onChange={(event) => setRename(event.currentTarget.value)} />
              <Button onClick={() => void renameCategory(id, rename).then(() => { setRenaming(false); void load(); })}>Save</Button>
            </Stack>
          </Modal>
          <Modal opened={mergeOpen} onClose={() => setMergeOpen(false)} title="Merge into">
            <Stack>
              {others.map((other) => (
                <Button key={other.id} variant="default" onClick={() => void mergeCategory(id, other.id).then(() => navigate(`/inventory/categories/${other.id}`))}>
                  {other.name}
                </Button>
              ))}
            </Stack>
          </Modal>
        </>
      )}
    </Stack>
  );
}
