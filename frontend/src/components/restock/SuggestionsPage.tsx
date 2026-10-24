import { useCallback, useEffect, useState } from 'react';
import { Link } from 'react-router-dom';
import { Alert, Anchor, Badge, Button, Checkbox, Group, Loader, Stack, Text, Title } from '@mantine/core';
import { applyCategorySuggestions, dismissCategorySuggestions, getCategorySuggestions } from '../../api/client';
import type { SuggestionCard } from '../../restock/types';
import { restockCopy } from '../../restock/copy';
import { inlineFailure } from '@stockbot/shared/contract';
import './restock.css';

export function SuggestionsPage() {
  const [cards, setCards] = useState<SuggestionCard[]>([]);
  const [picked, setPicked] = useState<Record<string, string[]>>({});
  const [loading, setLoading] = useState(true);
  const [error, setError] = useState('');

  const load = useCallback(async () => {
    setLoading(true);
    setError('');
    try {
      const next = await getCategorySuggestions();
      setCards(next);
      const initial: Record<string, string[]> = {};
      for (const card of next) {
        initial[card.category] = card.items.filter((item) => item.checked).map((item) => item.itemId);
      }
      setPicked(initial);
    } catch (requestError) {
      setError(inlineFailure(requestError, 'Unable to load suggestions.') ?? '');
    } finally {
      setLoading(false);
    }
  }, []);

  useEffect(() => {
    void Promise.resolve().then(load);
  }, [load]);

  const toggle = (category: string, itemId: string, on: boolean) => {
    setPicked((current) => {
      const ids = new Set(current[category] ?? []);
      if (on) ids.add(itemId);
      else ids.delete(itemId);
      return { ...current, [category]: [...ids] };
    });
  };

  if (loading) return <Loader aria-label="Loading suggestions" />;

  return (
    <Stack gap="md" className="shelf-page">
      <Anchor component={Link} to="/settings" className="history-back">Settings</Anchor>
      <div>
        <Title order={1} size="h3">Category suggestions</Title>
        <Text c="dimmed" size="sm">From Open Food Facts tags and product names. {restockCopy.nothingChanges}</Text>
      </div>
      {error !== '' && <Alert color="red">{error}</Alert>}
      {cards.length === 0 && <Text c="dimmed">No suggestions right now.</Text>}
      {cards.map((card) => {
        const chosen = picked[card.category] ?? [];
        return (
          <section key={card.category} className="restock-card">
            <Group justify="space-between" align="flex-start">
              <div>
                <Title order={2} size="h4">{card.category}</Title>
                <Text size="xs" c="dimmed">
                  {`${card.items.length} items`}
                  {card.freshOnly ? ' · fresh only' : ''}
                  {card.new ? '' : ' · already in your categories'}
                </Text>
              </div>
              {card.new && <Badge variant="light">New</Badge>}
            </Group>
            {card.note && <p className="restock-note">{card.note}</p>}
            <Stack gap={0} mt="xs">
              {card.items.map((item) => (
                <Checkbox
                  key={item.itemId}
                  checked={chosen.includes(item.itemId)}
                  onChange={(event) => toggle(card.category, item.itemId, event.currentTarget.checked)}
                  label={<span>{item.name}<span className="restock-quiet"> {item.reason}</span></span>}
                  my={6}
                />
              ))}
            </Stack>
            {card.notSuggested && card.notSuggested.length > 0 && (
              <Text size="xs" c="dimmed" mt="xs">{`Not suggested: ${card.notSuggested.join(', ')}`}</Text>
            )}
            <Group mt="sm">
              <Button
                size="xs"
                disabled={chosen.length === 0}
                onClick={() => void applyCategorySuggestions(card.category, chosen).then(() => load())}
              >
                {`Add ${chosen.length} to ${card.category}`}
              </Button>
              <Button size="xs" variant="default" onClick={() => void dismissCategorySuggestions(card.category, card.items.map((item) => item.itemId)).then(setCards)}>
                Not now
              </Button>
            </Group>
          </section>
        );
      })}
    </Stack>
  );
}
