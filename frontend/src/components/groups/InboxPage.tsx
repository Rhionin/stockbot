import { useCallback, useEffect, useState } from 'react';
import { Link } from 'react-router-dom';
import {
  Alert, Anchor, Button, Checkbox, Group, Loader, NumberInput, Stack, Text, TextInput, Title,
} from '@mantine/core';
import { ApiError, acceptSuggestion, dismissSuggestion, listSuggestions, rescanSuggestions, skipSuggestion } from '../../api/client';
import type { GroupSuggestion, GroupTarget, SuggestionMember, TargetConflictMember } from '../../types';
import { conflictLine, kindPhrase, memberFacts } from './copy';
import { inlineFailure } from '../../api/contract';

const MemberPhoto = ({ src }: { src?: string }) => {
  const [failed, setFailed] = useState(false);
  if (!src || failed) {
    return <div className="suggestion-photo suggestion-photo-empty">No photo</div>;
  }
  return <img src={src} alt="" className="suggestion-photo" onError={() => setFailed(true)} />;
};

const MemberCard = ({
  member,
  facts,
  checked,
  onChecked,
}: {
  member: SuggestionMember;
  facts: ReturnType<typeof memberFacts>;
  checked: boolean;
  onChecked: (on: boolean) => void;
}) => (
  <article className="suggestion-member" data-included={checked ? 'true' : 'false'}>
    <Checkbox
      label="Include"
      aria-label={member.name}
      checked={checked}
      onChange={(event) => onChecked(event.currentTarget.checked)}
    />
    <MemberPhoto src={member.imageUrl} />
    <Title order={2} size="h5" className="suggestion-name">{member.name}</Title>
    {facts.length > 0 && (
      <dl className="suggestion-facts">
        {facts.map((fact) => {
          const value = fact.text(member);
          return (
            <div key={fact.key}>
              <dt>{fact.label}</dt>
              <dd className={value === '' ? 'suggestion-missing' : undefined}>{value === '' ? 'Not listed' : value}</dd>
            </div>
          );
        })}
      </dl>
    )}
    {member.caution !== '' && <Text size="sm" c="dimmed">{member.caution}</Text>}
  </article>
);

export const InboxPage = ({ embedded = false, focusId }: { embedded?: boolean; focusId?: string }) => {
  const [cards, setCards] = useState<GroupSuggestion[]>([]);
  const [index, setIndex] = useState(0);
  const [checks, setChecks] = useState<{ cardId: string; values: Record<string, boolean> } | null>(null);
  const [nameDraft, setNameDraft] = useState<{ cardId: string; name: string } | null>(null);
  const [conflictFor, setConflictFor] = useState<{ cardId: string; members: TargetConflictMember[] } | null>(null);
  const [months, setMonths] = useState<number | string>(3);
  const [ounces, setOunces] = useState<number | string>('');
  const [loading, setLoading] = useState(true);
  const [saving, setSaving] = useState(false);
  const [scanning, setScanning] = useState(false);
  const [notice, setNotice] = useState('');
  const [error, setError] = useState('');

  const load = useCallback(async () => {
    setError('');
    try {
      const rows = await listSuggestions();
      setCards(rows);
      setIndex((current) => (rows.length === 0 ? 0 : Math.min(current, rows.length - 1)));
    } catch (requestError) {
      setError(inlineFailure(requestError, 'Unable to load suggestions.') ?? '');
    } finally {
      setLoading(false);
    }
  }, []);

  useEffect(() => {
    void Promise.resolve().then(load);
  }, [load]);

  const [focusedId, setFocusedId] = useState<string | undefined>(undefined);
  const focusIndex = focusId ? cards.findIndex((row) => row.id === focusId) : -1;
  // Jump to the suggestion the inventory row asked for, once that card is loaded.
  if (focusId && focusId !== focusedId && focusIndex >= 0) {
    setFocusedId(focusId);
    setIndex(focusIndex);
  }

  const card = cards[index];
  const checked = checks !== null && card !== undefined && checks.cardId === card.id
    ? checks.values
    : Object.fromEntries((card?.members ?? []).map((member) => [member.productId, member.included]));
  const name = nameDraft !== null && card !== undefined && nameDraft.cardId === card.id ? nameDraft.name : (card?.title ?? '');
  const conflict = conflictFor !== null && card !== undefined && conflictFor.cardId === card.id ? conflictFor.members : null;
  const facts = memberFacts(card?.members ?? []);

  const setChecked = (productId: string, on: boolean) => {
    if (!card) return;
    setChecks({ cardId: card.id, values: { ...checked, [productId]: on } });
  };

  const selected = card ? card.members.filter((member) => checked[member.productId]).map((member) => member.productId) : [];

  const group = async (target?: GroupTarget) => {
    if (!card || selected.length === 0) return;
    setSaving(true);
    setError('');
    try {
      const renamed = name.trim() !== '' && name.trim() !== card.title ? name.trim() : undefined;
      await acceptSuggestion(card.id, selected, renamed, target);
      setConflictFor(null);
      await load();
    } catch (requestError) {
      if (requestError instanceof ApiError && requestError.code === 'target_decision_required') {
        setConflictFor({ cardId: card.id, members: requestError.members });
        return;
      }
      setError(inlineFailure(requestError, 'Unable to group these products.') ?? '');
    } finally {
      setSaving(false);
    }
  };

  const dismiss = async () => {
    if (!card) return;
    setSaving(true);
    setError('');
    try {
      await dismissSuggestion(card.id);
      await load();
    } catch (requestError) {
      setError(inlineFailure(requestError, 'Unable to dismiss that suggestion.') ?? '');
    } finally {
      setSaving(false);
    }
  };

  const rescan = async () => {
    setScanning(true);
    setNotice('');
    setError('');
    try {
      const created = await rescanSuggestions();
      if (created.length === 0) {
        setNotice('No new groups found');
        return;
      }
      await load();
    } catch (requestError) {
      setError(inlineFailure(requestError, 'Unable to look for groups.') ?? '');
    } finally {
      setScanning(false);
    }
  };

  const skip = async () => {
    if (!card) return;
    setError('');
    try {
      await skipSuggestion(card.id);
      setIndex((current) => (cards.length <= 1 ? 0 : (current + 1) % cards.length));
    } catch (requestError) {
      setError(inlineFailure(requestError, 'Unable to skip that suggestion.') ?? '');
    }
  };

  return (
    <Stack gap="sm" className="bin-page">
      {!embedded && <Anchor component={Link} to="/inventory?filter=groups" size="sm">Inventory</Anchor>}
      <Group justify="space-between" align="center" wrap="wrap">
        {!embedded && <Title order={1} size="h3">Suggestions</Title>}
        <Button size="sm" variant="default" className="suggestion-rescan" loading={scanning} onClick={() => void rescan()}>
          Look for more groups
        </Button>
      </Group>
      {notice !== '' && <Text size="sm">{notice}</Text>}
      {loading && <Loader aria-label="Loading suggestions" />}
      {error !== '' && <Alert color="red" py="xs">{error}</Alert>}
      {!loading && cards.length === 0 && <Text c="dimmed">Nothing to review.</Text>}
      {card && (
        <Stack gap="sm">
          <Text size="sm" c="dimmed">{index + 1} of {cards.length} · {kindPhrase(card.kind)}</Text>
          <Text size="sm">Uncheck a product that does not belong. Then group the rest, or dismiss the suggestion.</Text>
          {card.existingGroupId ? (
            <Text size="sm">These join {card.title}.</Text>
          ) : (
            <TextInput
              label="Group name"
              description="Edit the name before you group them."
              value={name}
              onChange={(event) => setNameDraft({ cardId: card.id, name: event.currentTarget.value })}
            />
          )}
          <div className="suggestion-compare">
            {card.members.map((member) => (
              <MemberCard
                key={member.productId}
                member={member}
                facts={facts}
                checked={checked[member.productId] === true}
                onChecked={(on) => setChecked(member.productId, on)}
              />
            ))}
          </div>
          {conflict !== null && (
            <Alert color="yellow" title="Choose what this group should keep on hand">
              <Stack gap="xs">
                {conflict.map((member) => <Text key={member.productId} size="sm">{conflictLine(member)}</Text>)}
                <Text size="sm">These products already have their own supply setting.</Text>
                <NumberInput label="Ounces" min={0} value={ounces} onChange={setOunces} w={140} />
                <NumberInput label="Months" min={1} max={12} allowDecimal={false} value={months} onChange={setMonths} w={140} />
                <Group gap="xs">
                  <Button size="sm" onClick={() => void group({ clear: true })}>Use the household default</Button>
                  <Button
                    size="sm"
                    variant="light"
                    onClick={() => {
                      const value = typeof ounces === 'number' ? ounces : Number(ounces);
                      if (!Number.isFinite(value) || value <= 0) return;
                      void group({ quantity: value, dimension: 'mass' });
                    }}
                  >
                    Use ounces
                  </Button>
                  <Button
                    size="sm"
                    variant="light"
                    onClick={() => {
                      const value = typeof months === 'number' ? months : Number(months);
                      if (!Number.isInteger(value) || value < 1 || value > 12) return;
                      void group({ windowMonths: value });
                    }}
                  >
                    Use months
                  </Button>
                </Group>
              </Stack>
            </Alert>
          )}
          <div className="suggestion-actions">
            <Button className="suggestion-primary" size="sm" loading={saving} disabled={selected.length === 0} onClick={() => void group()}>
              {card.existingGroupId ? 'Add to group' : 'Group these'}
            </Button>
            <Button className="suggestion-dismiss" size="sm" variant="default" loading={saving} onClick={() => void dismiss()}>Not the same</Button>
            <Button className="suggestion-skip" size="sm" variant="default" onClick={() => void skip()}>Skip for now</Button>
          </div>
        </Stack>
      )}
    </Stack>
  );
};
