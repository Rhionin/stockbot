import { useCallback, useEffect, useState } from 'react';
import {
  ActionIcon,
  Button,
  Group,
  Menu,
  Skeleton,
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
import { krogerDisabledOnProviderList } from '@stockbot/shared/providerList';
import {
  deleteKrogerApp,
  getKrogerApp,
  listProviders,
  postKrogerAppTest,
} from '../../../api/client';
import { KrogerAppSetupModal } from './KrogerAppSetupModal';
import { RemoveStoreAppDialog } from './RemoveStoreAppDialog';
import { startKrogerConnect } from './krogerConnect';
import {
  mapKrogerAppApiError,
  mapTestAgainResult,
  rowLoadFailureMessage,
} from './krogerAppMessages';
import './storeApp.css';

export interface StoreAppSectionProps {
  householdId: string;
  householdName: string;
  isAdmin: boolean;
  linkedStoreName?: string | null;
  canConnect?: boolean;
  setupStepOnOpen?: number;
  onSetupOpened?: () => void;
}

function isDisabled(response: KrogerAppGetResponse): response is { status: 'disabled' } {
  return 'status' in response && response.status === 'disabled';
}

export function StoreAppSection({
  householdId,
  householdName,
  isAdmin,
  linkedStoreName = null,
  canConnect = true,
  setupStepOnOpen,
  onSetupOpened,
}: StoreAppSectionProps) {
  const [app, setApp] = useState<KrogerApp | null>(null);
  const [disabled, setDisabled] = useState(false);
  const [loadError, setLoadError] = useState('');
  const [loading, setLoading] = useState(true);
  const [setupOpen, setSetupOpen] = useState(false);
  const [setupInitialStep, setSetupInitialStep] = useState<number | undefined>();
  const [replaceMode, setReplaceMode] = useState(false);
  const [removeOpen, setRemoveOpen] = useState(false);
  const [banner, setBanner] = useState('');
  const [testBusy, setTestBusy] = useState(false);
  const [testNotice, setTestNotice] = useState('');
  const [testNoticeAlert, setTestNoticeAlert] = useState(false);
  const [testRateLimited, setTestRateLimited] = useState(false);
  const [forcedMember, setForcedMember] = useState(false);

  const ctx = storeNameContext(linkedStoreName);
  const canManage = !forcedMember && (app?.canManage ?? isAdmin);

  const reload = useCallback(async () => {
    if (householdId === '') return;
    setLoading(true);
    setLoadError('');
    try {
      const [response, providers] = await Promise.all([
        getKrogerApp(householdId),
        listProviders().catch(() => [] as Awaited<ReturnType<typeof listProviders>>),
      ]);
      if (isDisabled(response) || krogerDisabledOnProviderList(providers)) {
        setDisabled(true);
        setApp(null);
        setSetupOpen(false);
        return;
      }
      setDisabled(false);
      setApp(response);
    } catch {
      setLoadError(rowLoadFailureMessage(null));
    } finally {
      setLoading(false);
    }
  }, [householdId]);

  useEffect(() => {
    void Promise.resolve().then(reload);
  }, [reload]);

  useEffect(() => {
    if (setupStepOnOpen === undefined) return;
    /* eslint-disable react-hooks/set-state-in-effect -- open setup once when OAuth returns */
    setSetupInitialStep(setupStepOnOpen);
    setSetupOpen(true);
    onSetupOpened?.();
    /* eslint-enable react-hooks/set-state-in-effect */
  }, [setupStepOnOpen, onSetupOpened]);

  if (disabled) return null;

  if (loading && app === null && loadError === '') {
    return (
      <section className="store-app-section" aria-labelledby="store-app-heading" aria-busy="true">
        <Title id="store-app-heading" order={3}>{krogerAppCopy.rowTitle}</Title>
        <Skeleton height={44} mt="sm" aria-hidden />
        <Skeleton height={20} mt="xs" width="80%" aria-hidden />
      </section>
    );
  }

  const openSetup = (replace = false, step?: number) => {
    setReplaceMode(replace);
    setSetupInitialStep(step);
    setSetupOpen(true);
  };

  const runTestAgain = async () => {
    if (testRateLimited) return;
    setTestBusy(true);
    setTestNotice('');
    setTestNoticeAlert(false);
    try {
      const result = await postKrogerAppTest(householdId);
      setTestRateLimited(false);
      const mapped = mapTestAgainResult(result, ctx);
      setTestNotice(mapped.message);
      setTestNoticeAlert(mapped.alert);
      if (result.ok) await reload();
    } catch (requestError) {
      const mapped = mapKrogerAppApiError(requestError);
      if (mapped.kind === 'store_disabled') {
        setDisabled(true);
        setApp(null);
        setSetupOpen(false);
        return;
      }
      if (mapped.kind === 'forbidden_role') {
        setForcedMember(true);
        await reload();
        return;
      }
      if (mapped.kind === 'not_configured') {
        await reload();
        return;
      }
      if (mapped.kind === 'rate_limited') {
        setTestRateLimited(true);
        setTestNotice(krogerAppCopy.errTooMany);
        setTestNoticeAlert(false);
        return;
      }
      if (mapped.kind === 'message') {
        setTestNotice(mapped.message);
        setTestNoticeAlert(mapped.alert);
      }
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

  const showProductAccessNotes = app?.configured
    && (status === 'ready_to_connect' || status === 'connected' || status === 'needs_reconnect');

  const primaryAction = () => {
    if (!canManage) return null;
    switch (status) {
      case 'not_set_up':
        return (
          <Button className="store-app-touch-target store-app-touch-target-wide" onClick={() => openSetup(false)}>
            {krogerAppCopy.rowSetUp}
          </Button>
        );
      case 'ready_to_connect':
        if (!canConnect) return null;
        return (
          <Button className="store-app-touch-target store-app-touch-target-wide" onClick={() => { void startKrogerConnect('household'); }}>
            {krogerAppCopy.rowConnect}
          </Button>
        );
      case 'needs_reconnect':
        if (!canConnect) return null;
        return (
          <Button className="store-app-touch-target store-app-touch-target-wide" onClick={() => { void startKrogerConnect('household'); }}>
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
    if (testBusy) return krogerAppCopy.testBusy;
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
                className="store-app-menu-trigger"
                variant="subtle"
                aria-label={krogerAppCopy.menuMore}
              >
                ⋯
              </ActionIcon>
            </Menu.Target>
            <Menu.Dropdown>
              <Menu.Item onClick={() => openSetup(true, 6)}>{krogerAppCopy.menuReplace}</Menu.Item>
              <Menu.Item onClick={() => { void runTestAgain(); }} disabled={testBusy || testRateLimited}>
                {testBusy ? krogerAppCopy.testBusy : krogerAppCopy.menuTest}
              </Menu.Item>
              <Menu.Item color="red" onClick={() => setRemoveOpen(true)}>{krogerAppCopy.menuRemove}</Menu.Item>
            </Menu.Dropdown>
          </Menu>
        )}
      </Group>

      {loadError !== '' && (
        <Stack gap="xs" mt="sm">
          <Text role="alert" c="red" size="sm">{krogerAppCopy.rowLoadFailed}</Text>
          <Button className="store-app-touch-target store-app-touch-target-wide" variant="default" onClick={() => { void reload(); }}>
            {krogerAppCopy.tryAgain}
          </Button>
        </Stack>
      )}

      {loadError === '' && app !== null && (
        <div className="store-app-row">
          <Stack gap={4} style={{ flex: 1 }}>
            <Text className="store-app-row-title">{statusLabel()}</Text>
            <Text size="sm">{bodyCopy()}</Text>
            {showProductAccessNotes && app.productAccess === 'false' && (
              <Text size="sm" className="store-app-note-unknown" role="status">
                {krogerAppRowNoChecks(ctx)}
              </Text>
            )}
            {showProductAccessNotes && app.productAccess === 'unknown' && canManage && (
              <Group gap="xs" align="flex-start" wrap="wrap" role="status">
                <Text size="sm" className="store-app-note-unknown" aria-hidden>
                  ⚠
                </Text>
                <Text size="sm" className="store-app-note-unknown">{krogerAppNoteUnknown(ctx)}</Text>
                <Button
                  variant="outline"
                  size="compact-sm"
                  className="store-app-touch-target store-app-touch-target-wide"
                  loading={testBusy}
                  disabled={testRateLimited}
                  aria-busy={testBusy}
                  onClick={() => { void runTestAgain(); }}
                >
                  {krogerAppCopy.menuTest}
                </Button>
              </Group>
            )}
            {showProductAccessNotes && app.productAccess === 'unknown' && !canManage && (
              <Group gap="xs" align="flex-start" wrap="wrap" role="status">
                <Text size="sm" className="store-app-note-unknown" aria-hidden>
                  ⚠
                </Text>
                <Text size="sm" className="store-app-note-unknown">{krogerAppNoteUnknown(ctx)}</Text>
              </Group>
            )}
            {testNotice !== '' && (
              <Text role={testNoticeAlert ? 'alert' : 'status'} c={testNoticeAlert ? 'red' : undefined} size="sm">
                {testNotice}
              </Text>
            )}
            {banner !== '' && <Text role="status" size="sm">{banner}</Text>}
          </Stack>
          {primaryAction()}
        </div>
      )}

      {app !== null && (
        <KrogerAppSetupModal
          opened={setupOpen}
          onClose={() => {
            setSetupOpen(false);
            setSetupInitialStep(undefined);
          }}
          householdId={householdId}
          householdName={householdName}
          linkedStoreName={linkedStoreName}
          app={app}
          replaceMode={replaceMode}
          initialStep={setupInitialStep}
          onSaved={onSaved}
          onStoreDisabled={() => {
            setDisabled(true);
            setApp(null);
          }}
          onForbiddenRole={() => {
            setForcedMember(true);
            void reload();
          }}
          onNotConfigured={() => { void reload(); }}
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
