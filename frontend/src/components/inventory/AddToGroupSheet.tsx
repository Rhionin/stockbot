import { useMemo, useState } from 'react';
import { useNavigate } from 'react-router-dom';
import { Button, Drawer, Stack, Text, TextInput, UnstyledButton } from '@mantine/core';
import { addGroupMembers, ApiError, createGroup } from '../../api/client';
import type { GroupSuggestion, GroupTarget, ProductGroup, TargetConflictMember } from '../../types';
import { TargetConflictPrompt } from '../groups/TargetConflictPrompt';
import { suggestedDestinations } from './shelf';
import { inlineFailure } from '../../api/contract';

export interface AddToGroupSheetProps {
  opened: boolean;
  productId: string;
  productName: string;
  fromGroupId?: string;
  groups: ProductGroup[];
  suggestions: GroupSuggestion[];
  onClose: () => void;
  onChanged: () => void;
}

export function AddToGroupSheet({
  opened,
  productId,
  productName,
  fromGroupId,
  groups,
  suggestions,
  onClose,
  onChanged,
}: AddToGroupSheetProps) {
  const navigate = useNavigate();
  const moving = Boolean(fromGroupId);
  const [query, setQuery] = useState('');
  const [conflict, setConflict] = useState<{ group: ProductGroup; members: TargetConflictMember[] } | null>(null);
  const [error, setError] = useState('');
  const [saving, setSaving] = useState(false);

  const available = useMemo(
    () => groups.filter((group) => group.id !== fromGroupId),
    [groups, fromGroupId],
  );
  const suggested = useMemo(
    () => suggestedDestinations(productId, suggestions, available),
    [productId, suggestions, available],
  );
  const q = query.trim().toLowerCase();
  const matches = (name: string) => q === '' || name.toLowerCase().includes(q);
  const suggestedShown = suggested.filter((group) => matches(group.name));
  const suggestedIds = new Set(suggested.map((group) => group.id));
  const rest = available.filter((group) => !suggestedIds.has(group.id) && matches(group.name));

  const reset = () => {
    setQuery('');
    setConflict(null);
    setError('');
    setSaving(false);
  };

  const close = () => {
    reset();
    onClose();
  };

  const add = async (group: ProductGroup, target?: GroupTarget) => {
    if (saving) return;
    setSaving(true);
    setError('');
    try {
      await addGroupMembers(group.id, [productId], fromGroupId ?? '', target);
      reset();
      onChanged();
    } catch (requestError) {
      if (requestError instanceof ApiError && requestError.code === 'target_decision_required') {
        setConflict({ group, members: requestError.members });
        return;
      }
      setError(inlineFailure(requestError, 'Unable to update that group.') ?? '');
    } finally {
      setSaving(false);
    }
  };

  const create = async () => {
    if (saving) return;
    setSaving(true);
    setError('');
    try {
      const created = await createGroup(productName, [productId]);
      reset();
      onChanged();
      navigate(`/groups/${created.id}`);
    } catch (requestError) {
      setError(inlineFailure(requestError, 'Unable to create the group.') ?? '');
      setSaving(false);
    }
  };

  return (
    <Drawer.Root opened={opened} onClose={close} position="bottom" transitionProps={{ duration: 0 }}>
      <Drawer.Overlay />
      <Drawer.Content className="bin-page shelf-sheet">
        {/* A header element here is a second banner landmark beside the app bar. */}
        <Drawer.Header component="div">
          <Drawer.Title>{moving ? `Move ${productName} to…` : `Add ${productName} to…`}</Drawer.Title>
          <Drawer.CloseButton aria-label="Close" />
        </Drawer.Header>
        <Drawer.Body className="shelf-sheet-body">
          <Stack gap="sm">
        {moving && (
          <Text size="sm">Already in {groups.find((group) => group.id === fromGroupId)?.name}.</Text>
        )}
        <TextInput
          label="Search groups"
          placeholder="Name"
          value={query}
          onChange={(event) => setQuery(event.currentTarget.value)}
        />
        {conflict && (
          <TargetConflictPrompt
            group={conflict.group}
            members={conflict.members}
            onChoose={(target) => void add(conflict.group, target)}
            onCancel={() => setConflict(null)}
          />
        )}
        {conflict === null && suggestedShown.length > 0 && (
          <Stack gap={4} aria-label="Suggested groups">
            {suggestedShown.map((group) => (
              <UnstyledButton
                key={group.id}
                className="shelf-sheet-choice"
                disabled={saving}
                onClick={() => {
                  const full = available.find((item) => item.id === group.id);
                  if (full) void add(full);
                }}
              >
                <span className="shelf-sheet-choice-name">{group.name}</span>
                <span className="shelf-sheet-choice-reason">{group.reason}</span>
              </UnstyledButton>
            ))}
          </Stack>
        )}
        {conflict === null && rest.map((group) => (
          <UnstyledButton
            key={group.id}
            className="shelf-sheet-choice"
            disabled={saving}
            onClick={() => void add(group)}
          >
            <span className="shelf-sheet-choice-name">{group.name}</span>
          </UnstyledButton>
        ))}
        {conflict === null && available.length > 0 && suggestedShown.length === 0 && rest.length === 0 && (
          <Text size="sm" c="dimmed">No matching groups.</Text>
        )}
        {conflict === null && available.length === 0 && (
          <Text size="sm" c="dimmed">{moving ? 'No other groups.' : 'No groups yet.'}</Text>
        )}
        {conflict === null && !moving && (
          <Button variant="default" className="shelf-select" loading={saving} onClick={() => void create()}>
            {`New group "${productName}"`}
          </Button>
        )}
            {error !== '' && <Text size="sm" c="red">{error}</Text>}
          </Stack>
        </Drawer.Body>
      </Drawer.Content>
    </Drawer.Root>
  );
}
