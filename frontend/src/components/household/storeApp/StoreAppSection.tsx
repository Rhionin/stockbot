import { useCallback, useEffect, useState } from 'react';
import {
  ActionIcon,
  Alert,
  Button,
  Group,
  Menu,
  Stack,
  Text,
  Title,
} from '@mantine/core';
import {
  krogerAppCopy,
  krogerAppConnectLabel,
  krogerAppNoteUnknown,
  krogerAppRowConnectedBody,
  krogerAppRowNoChecks,
  krogerAppRowReadyBody,
  storeNameContext,
} from '@stockbot/shared/krogerAppCopy';
import type { KrogerApp, KrogerAppGetResponse } from '@stockbot/shared/types';
import { inlineFailure } from '@stockbot/shared/contract';
import {
  deleteKrogerApp,
  getKrogerApp,
  postKrogerAppTest,
} from '../../../api/client';
import { KrogerAppSetupModal } from './KrogerAppSetupModal';
import { RemoveStoreAppDialog } from './RemoveStoreAppDialog';
import { startKrogerConnect } from './krogerConnect';
import './storeApp.css';

export interface StoreAppSectionProps {
  householdId: string;
  householdName: string;
  isAdmin: boolean;
  linkedStoreName?: string | null;
}

function isDisabled(response: KrogerAppGetResponse): response is { status: 'disabled' } {
  return 'status' in response && response.status === 'disabled';
}

export function StoreAppSection({
  householdId,
  householdName,
  isAdmin,
  linkedStoreName = null,
}: StoreAppSectionProps) {
  const [app, setApp] = useState<KrogerApp | null>(null);
  const [disabled, setDisabled] = useState(false);
  const [loadError, setLoadError] = useState('');
  const [loading, setLoading] = useState(true);
  const [setupOpen, setSetupOpen] = useState(false);
  const [replaceMode, setReplaceMode] = useState(false);
  const [removeOpen, setRemoveOpen] = useState(false);
  const [banner, setBanner] = useState('');
  const [testBusy, setTestBusy] = useState(false);
  const [testNotice, setTestNotice] = useState('');

  const ctx = storeNameContext(linkedStoreName);
  const canManage = app?.canManage ?? isAdmin;

  const reload = useCallback(async () => {
    if (householdId === '') return;
    setLoading(true);
    setLoadError('');
    try {
      const response = await getKrogerApp(householdId);
      if (isDisabled(response)) {
        setDisabled(true);
        setApp(null);
        return;
      }
      setDisabled(false);
      setApp(response);
    } catch (requestError) {
      setLoadError(inlineFailure(requestError, krogerAppCopy.rowLoadFailed) ?? krogerAppCopy.rowLoadFailed);
    } finally {
      setLoading(false);
    }
  }, [householdId]);

  useEffect(() => {
    void Promise.resolve().then(reload);
  }, [reload]);

  if (disabled || (loading && app === null && loadError === '')) {
    if (disabled) return null;
    if (loading) return null;
  }

  const openSetup = (replace = false) => {
    setReplaceMode(replace);
    setSetupOpen(true);
  };

  const runTestAgain = async () => {
    setTestBusy(true);
    setTestNotice('');
    try {
      const result = await postKrogerAppTest(householdId, {});
      if (result.ok) {
        setTestNotice(krogerAppCopy.testOk);
        await reload();
      } else {
        setTestNotice(krogerAppCopy.errWrongKeys);
      }
    } catch (requestError) {
      setTestNotice(inlineFailure(requestError, krogerAppCopy.rowLoadFailed) ?? krogerAppCopy.rowLoadFailed);
    } finally {
      setTestBusy(false);
    }
  };

  const removeApp = async () => {
    await deleteKrogerApp(householdId);
    setBanner(krogerAppCopy.rmDone);
    await reload();
  };

  const onSaved = () => {
    void reload();
  };

  const status = app?.status ?? 'not_set_up';

  const primaryAction = () => {
    if (!canManage) return null;
    switch (status) {
      case 'not_set_up':
        return (
          <Button className="store-app-touch-target" onClick={() => openSetup(false)}>
            {krogerAppCopy.rowSetUp}
          </Button>
        );
      case 'ready_to_connect':
        return (
          <Button className="store-app-touch-target" onClick={() => { void startKrogerConnect('household'); }}>
            {krogerAppCopy.rowConnect}
          </Button>
        );
      case 'needs_reconnect':
        return (
          <Button className="store-app-touch-target" onClick={() => { void startKrogerConnect('household'); }}>
            {krogerAppConnectLabel(ctx, true)}
          </Button>
        );
      default:
        return null;
    }
  };

  const bodyCopy = () => {
    switch (status) {
      case 'not_set_up':
        return canManage ? krogerAppCopy.rowWhyEmpty : krogerAppCopy.askAdmin;
      case 'ready_to_connect':
        return canManage
          ? krogerAppRowReadyBody(ctx)
          : (ctx.store === 'your store'
            ? krogerAppCopy.askAdminConnectFallback
            : krogerAppCopy.askAdminConnect.replaceAll('{store}', ctx.store));
      case 'connected':
        return krogerAppRowConnectedBody(ctx);
      case 'needs_reconnect':
        return canManage
          ? (ctx.store === 'your store' ? krogerAppCopy.rowNeedsBodyFallback : krogerAppCopy.rowNeedsBody.replaceAll('{store}', ctx.store))
          : (ctx.store === 'your store'
            ? krogerAppCopy.askAdminConnectFallback
            : krogerAppCopy.askAdminConnect.replaceAll('{store}', ctx.store));
      default:
        return '';
    }
  };

  const statusLabel = () => {
    switch (status) {
      case 'not_set_up': return krogerAppCopy.rowNotSetUp;
      case 'ready_to_connect': return krogerAppCopy.rowReady;
      case 'connected': return krogerAppCopy.rowConnected;
      case 'needs_reconnect': return krogerAppCopy.rowNeeds;
      default: return '';
    }
  };

  return (
    <section className="store-app-section" aria-labelledby="store-app-heading">
      <Group justify="space-between" align="flex-start" wrap="nowrap">
        <Title id="store-app-heading" order={3}>{krogerAppCopy.rowTitle}</Title>
        {app?.configured && canManage && (
          <Menu position="bottom-end" withinPortal>
            <Menu.Target>
              <ActionIcon
                className="store-app-touch-target"
                variant="subtle"
                aria-label={krogerAppCopy.menuMore}
              >
                ⋯
              </ActionIcon>
            </Menu.Target>
            <Menu.Dropdown>
              <Menu.Item onClick={() => openSetup(true)}>{krogerAppCopy.menuReplace}</Menu.Item>
              <Menu.Item onClick={() => { void runTestAgain(); }} disabled={testBusy}>
                {testBusy ? krogerAppCopy.testBusy : krogerAppCopy.menuTest}
              </Menu.Item>
              <Menu.Item color="red" onClick={() => setRemoveOpen(true)}>{krogerAppCopy.menuRemove}</Menu.Item>
            </Menu.Dropdown>
          </Menu>
        )}
      </Group>

      {loadError !== '' && (
        <Stack gap="xs" mt="sm">
          <Alert color="red" variant="light" role="alert">{krogerAppCopy.rowLoadFailed}</Alert>
          <Button className="store-app-touch-target" variant="default" onClick={() => { void reload(); }}>
            {krogerAppCopy.tryAgain}
          </Button>
        </Stack>
      )}

      {loadError === '' && app !== null && (
        <div className="store-app-row">
          <Stack gap={4} style={{ flex: 1 }}>
            <Text className="store-app-row-title">{statusLabel()}</Text>
            <Text size="sm">{bodyCopy()}</Text>
            {status === 'connected' && app.productAccess === 'false' && (
              <Text size="sm">{krogerAppRowNoChecks(ctx)}</Text>
            )}
            {status === 'connected' && app.productAccess === 'unknown' && (
              <Text size="sm">{krogerAppNoteUnknown(ctx)}</Text>
            )}
            {testNotice !== '' && <Text role="status" size="sm">{testNotice}</Text>}
            {banner !== '' && <Text role="status" size="sm">{banner}</Text>}
          </Stack>
          {primaryAction()}
        </div>
      )}

      {app !== null && (
        <KrogerAppSetupModal
          opened={setupOpen}
          onClose={() => setSetupOpen(false)}
          householdId={householdId}
          householdName={householdName}
          linkedStoreName={linkedStoreName}
          app={app}
          replaceMode={replaceMode}
          onSaved={onSaved}
        />
      )}

      <RemoveStoreAppDialog
        opened={removeOpen}
        onClose={() => setRemoveOpen(false)}
        linkedStoreName={linkedStoreName}
        onConfirm={removeApp}
      />
    </section>
  );
}
