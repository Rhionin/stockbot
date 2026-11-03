import { useCallback, useEffect, useState } from 'react';
import { Badge, Burger, Indicator, Menu } from '@mantine/core';
import { useNavigate } from 'react-router-dom';
import { listSuggestions } from '../../api/client';
import { useHouseholdSession } from '../auth/householdSession';
import { BuildStamp } from '../build/BuildStamp';
import { inlineFailure } from '@stockbot/shared/contract';

export interface AppMenuProps {
  onCredentialsChanged: () => void;
}

const suggestionReviewLabel = (count: number) =>
  `Review ${count} group ${count === 1 ? 'suggestion' : 'suggestions'}`;

export const AppMenu = ({ onCredentialsChanged }: AppMenuProps) => {
  void onCredentialsChanged;
  const navigate = useNavigate();
  const session = useHouseholdSession();
  const [opened, setOpened] = useState(false);
  const [loaded, setLoaded] = useState(false);
  const [loadError, setLoadError] = useState('');
  const [suggestionCount, setSuggestionCount] = useState(0);

  const reload = useCallback(async () => {
    try {
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

  const stage1 = session.auth?.kind === 'account';

  return (
    <Menu
      opened={opened}
      onChange={(next) => {
        setOpened(next);
        if (next) void reload();
      }}
      position="bottom-end"
      width={320}
      shadow="md"
      withinPortal
    >
      <Menu.Target>
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
        {!loaded && loadError === '' && (
          <Menu.Item disabled>Loading menu</Menu.Item>
        )}
        {loaded && loadError !== '' && (
          <Menu.Item disabled>{loadError}</Menu.Item>
        )}
        <Menu.Item onClick={() => navigate('/inventory?filter=groups&rescan=1')}>Look for more groups</Menu.Item>
        <Menu.Item onClick={() => navigate('/diagnostics')}>Diagnostics</Menu.Item>
        {stage1 && (
          <>
            <Menu.Item onClick={() => { setOpened(false); navigate('/household'); }}>Household</Menu.Item>
            <Menu.Item onClick={() => { setOpened(false); navigate('/households'); }}>Other households</Menu.Item>
            <Menu.Item onClick={() => { setOpened(false); navigate('/devices'); }}>Devices</Menu.Item>
          </>
        )}
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
  );
};
