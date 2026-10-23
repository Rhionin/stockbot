import { useCallback, useEffect, useState } from 'react';
import { Badge, Burger, Indicator, Menu } from '@mantine/core';
import { useNavigate } from 'react-router-dom';
import { disconnectProvider, listProviders, listSuggestions } from '../../api/client';
import { useHouseholdSession } from '../auth/householdSession';
import type { ProviderInfo } from '../../types';
import { BuildStamp } from '../build/BuildStamp';
import { CredentialsDialog } from '../shopping/CredentialsDialog';
import { inlineFailure } from '../../api/contract';

export interface AppMenuProps {
  onCredentialsChanged: () => void;
}

const canEditCredentials = (provider: ProviderInfo) =>
  provider.capabilities.auth === 'oauth2_authorization_code';

const canDisconnect = (provider: ProviderInfo) =>
  canEditCredentials(provider)
  && provider.credentialsConfigured
  && provider.connectionState === 'connected';

const suggestionReviewLabel = (count: number) =>
  `Review ${count} group ${count === 1 ? 'suggestion' : 'suggestions'}`;

export const AppMenu = ({ onCredentialsChanged }: AppMenuProps) => {
  const navigate = useNavigate();
  const session = useHouseholdSession();
  const [opened, setOpened] = useState(false);
  const [providers, setProviders] = useState<ProviderInfo[]>([]);
  const [loaded, setLoaded] = useState(false);
  const [loadError, setLoadError] = useState('');
  const [disconnectError, setDisconnectError] = useState('');
  const [disconnectingId, setDisconnectingId] = useState<string | null>(null);
  const [editingId, setEditingId] = useState<string | null>(null);
  const [suggestionCount, setSuggestionCount] = useState(0);

  const reload = useCallback(async () => {
    try {
      const rows = await listProviders();
      setProviders(rows);
      setLoadError('');
      try {
        const cards = await listSuggestions();
        setSuggestionCount(cards.length);
      } catch {
        setSuggestionCount(0);
      }
    } catch (requestError) {
      setLoadError(inlineFailure(requestError, 'Unable to load store settings.') ?? '');
    } finally {
      setLoaded(true);
    }
  }, []);

  useEffect(() => {
    void Promise.resolve().then(reload);
  }, [reload]);

  const disconnect = async (provider: ProviderInfo) => {
    setDisconnectingId(provider.id);
    setDisconnectError('');
    try {
      await disconnectProvider(provider.id);
      await reload();
      onCredentialsChanged();
      setEditingId(null);
      setOpened(false);
    } catch (requestError) {
      setDisconnectError(inlineFailure(requestError, 'Unable to disconnect.') ?? '');
    } finally {
      setDisconnectingId(null);
    }
  };

  const editable = providers.filter(canEditCredentials);
  const editing = editable.find((provider) => provider.id === editingId) ?? null;

  return (
    <>
      <Menu
        opened={opened}
        onChange={(next) => {
          setOpened(next);
          if (next) {
            void reload();
          }
        }}
        position="bottom-end"
        width={320}
        shadow="md"
        withinPortal
      >
        <Menu.Target>
          {/* A smaller offset hangs a two-digit count off the corner and clips it on a phone. */}
          <Indicator disabled={suggestionCount === 0} label={suggestionCount} size={16} color="red" offset={12}>
            <Burger className="app-menu-button" opened={opened} size="sm" aria-label="Menu" />
          </Indicator>
        </Menu.Target>
        <Menu.Dropdown>
          {suggestionCount > 0 && (
            <Menu.Item
              className="app-menu-suggestions"
              rightSection={(
                <Badge className="app-menu-suggestion-count" color="red" variant="filled" size="sm" aria-hidden>
                  {suggestionCount}
                </Badge>
              )}
              onClick={() => {
                setOpened(false);
                navigate('/inventory?review=1');
              }}
            >
              {suggestionReviewLabel(suggestionCount)}
            </Menu.Item>
          )}
          {editable.map((provider) => (
            <Menu.Item
              key={provider.id}
              onClick={() => {
                setDisconnectError('');
                setEditingId(provider.id);
              }}
            >
              {`Manage ${provider.displayName} connection`}
            </Menu.Item>
          ))}
          {!loaded && editable.length === 0 && (
            <Menu.Item disabled>Loading store settings</Menu.Item>
          )}
          {loaded && loadError !== '' && editable.length === 0 && (
            <Menu.Item disabled>{loadError}</Menu.Item>
          )}
          <Menu.Item onClick={() => navigate('/inventory?filter=groups&rescan=1')}>Look for more groups</Menu.Item>
          <Menu.Item onClick={() => navigate('/diagnostics')}>Diagnostics</Menu.Item>
          <div className="app-menu-about">
            <Menu.Divider />
            <Menu.Item onClick={() => navigate('/settings')}>Settings</Menu.Item>
            <Menu.Label className="app-menu-about-label">Build</Menu.Label>
            <div className="app-menu-build">
              <BuildStamp />
            </div>
          </div>
          {session.required && (
            <Menu.Item
              onClick={() => {
                setOpened(false);
                session.logout();
              }}
            >
              Log out
            </Menu.Item>
          )}
        </Menu.Dropdown>
      </Menu>
      <CredentialsDialog
        provider={editing}
        opened={editing !== null}
        onClose={() => {
          setEditingId(null);
          setDisconnectError('');
        }}
        onChanged={() => {
          void reload().then(() => onCredentialsChanged());
        }}
        onDisconnect={editing !== null && canDisconnect(editing)
          ? () => {
              if (editing !== null) void disconnect(editing);
            }
          : undefined}
        disconnecting={editing !== null && disconnectingId === editing.id}
        disconnectError={disconnectError}
      />
    </>
  );
};
