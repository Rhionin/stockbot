import { useCallback, useEffect, useMemo, useRef, useState } from 'react';
import {
  Alert,
  Anchor,
  Button,
  Collapse,
  Group,
  Modal,
  NativeSelect,
  PasswordInput,
  Stack,
  Text,
  TextInput,
  Title,
} from '@mantine/core';
import { krogerAppCopy } from '@stockbot/shared/krogerAppCopy';
import { krogerLabels } from '@stockbot/shared/krogerLabels';
import {
  assertProgressHasNoSecrets,
  clearKrogerAppSetupProgress,
  readKrogerAppSetupProgress,
  writeKrogerAppSetupProgress,
  type KrogerAppSetupProgress,
  type KrogerAppSetupReturnTo,
} from '@stockbot/shared/krogerAppSetupProgress';
import { suggestedKrogerAppName } from '@stockbot/shared/krogerAppSuggestedName';
import type { KrogerApp, KrogerAppSaved } from '@stockbot/shared/types';
import { inlineFailure } from '@stockbot/shared/contract';
import { putKrogerApp } from '../../../api/client';
import { KrogerAppCopyRow } from './KrogerAppCopyRow';
import { startKrogerConnect } from './krogerConnect';
import {
  putFailureMessage,
  redirectMismatchSaveEnabled,
  storeNameCtx,
  testCodeMessage,
} from './krogerAppMessages';
import './storeApp.css';

const TOTAL_STEPS = 7;

export interface KrogerAppSetupModalProps {
  opened: boolean;
  onClose: () => void;
  householdId: string;
  householdName: string;
  linkedStoreName?: string | null;
  app: KrogerApp;
  replaceMode?: boolean;
  initialStep?: number;
  returnTo?: KrogerAppSetupReturnTo;
  onSaved: (saved: KrogerAppSaved) => void;
}

export function KrogerAppSetupModal({
  opened,
  onClose,
  householdId,
  householdName,
  linkedStoreName,
  app,
  replaceMode = false,
  initialStep,
  returnTo = 'household',
  onSaved,
}: KrogerAppSetupModalProps) {
  const ctx = storeNameCtx(linkedStoreName);
  const [step, setStep] = useState(1);
  const [done, setDone] = useState<number[]>([]);
  const [suggestedName, setSuggestedName] = useState('');
  const [clientId, setClientId] = useState('');
  const [clientSecret, setClientSecret] = useState('');
  const [modality, setModality] = useState(app.modality ?? 'PICKUP');
  const [showSecret, setShowSecret] = useState(false);
  const [stepsOpen, setStepsOpen] = useState(false);
  const [status, setStatus] = useState('');
  const [error, setError] = useState('');
  const [fieldError, setFieldError] = useState('');
  const [testing, setTesting] = useState(false);
  const [savedOk, setSavedOk] = useState(false);
  const [closeConfirm, setCloseConfirm] = useState(false);
  const [resumeNotice, setResumeNotice] = useState('');
  const typedSecrets = useRef(false);

  const redirectUri = app.redirectUri ?? '';
  const noAddress = redirectUri === '';

  const persistProgress = useCallback((next: Partial<KrogerAppSetupProgress> & { step: number }) => {
    const progress: KrogerAppSetupProgress = {
      step: next.step,
      done: next.done ?? done,
      suggestedName: next.suggestedName ?? suggestedName,
      returnTo,
      updatedAt: new Date().toISOString(),
    };
    assertProgressHasNoSecrets(progress);
    writeKrogerAppSetupProgress(householdId, progress);
  }, [done, householdId, returnTo, suggestedName]);

  const resetFields = () => {
    setClientId('');
    setClientSecret('');
    setShowSecret(false);
    setSavedOk(false);
    setStatus('');
    setError('');
    setFieldError('');
    typedSecrets.current = false;
  };

  useEffect(() => {
    if (!opened) return;
    void suggestedKrogerAppName(householdName, householdId).then(setSuggestedName);
    const stored = readKrogerAppSetupProgress(householdId);
    const startStep = initialStep
      ?? (replaceMode ? 6 : stored?.step ?? 1);
    /* eslint-disable react-hooks/set-state-in-effect -- reset wizard when the modal opens */
    if (stored && !replaceMode && !initialStep && stored.step > 1) {
      setResumeNotice(krogerAppCopy.resume(stored.step));
      setDone(stored.done ?? []);
    } else {
      setResumeNotice('');
      setDone([]);
    }
    setStep(Math.min(Math.max(startStep, 1), TOTAL_STEPS));
    setModality(app.modality ?? 'PICKUP');
    resetFields();
    setCloseConfirm(false);
    /* eslint-enable react-hooks/set-state-in-effect */
  }, [opened, householdId, householdName, initialStep, replaceMode, app.modality]);

  const markDone = (n: number) => {
    setDone((current) => (current.includes(n) ? current : [...current, n].sort((a, b) => a - b)));
  };

  const goStep = (n: number) => {
    const clamped = Math.min(Math.max(n, 1), TOTAL_STEPS);
    setStep(clamped);
    persistProgress({ step: clamped });
    setError('');
    setFieldError('');
  };

  const requestClose = () => {
    if (typedSecrets.current && !savedOk) {
      setCloseConfirm(true);
      return;
    }
    onClose();
  };

  const confirmClose = () => {
    setCloseConfirm(false);
    onClose();
  };

  const validateKeys = (): boolean => {
    if (clientId.trim() === '') {
      setFieldError(krogerAppCopy.vIdEmpty);
      return false;
    }
    if (clientSecret.trim() === '') {
      setFieldError(krogerAppCopy.vSecretEmpty);
      return false;
    }
    if (/\s/.test(clientId) || /\s/.test(clientSecret)) {
      setFieldError(krogerAppCopy.vInvalid);
      return false;
    }
    setFieldError('');
    return true;
  };

  const testAndSave = async () => {
    if (!validateKeys()) return;
    setTesting(true);
    setError('');
    setStatus('');
    try {
      const saved = await putKrogerApp(householdId, {
        clientId: clientId.trim(),
        clientSecret: clientSecret.trim(),
        modality,
      });
      if (!saved.test.ok) {
        setError(testCodeMessage(saved.test.code, ctx, replaceMode || app.configured));
        return;
      }
      if (saved.test.code === 'redirect_mismatch' && !redirectMismatchSaveEnabled && !replaceMode) {
        setError(testCodeMessage('redirect_mismatch', ctx, false));
        return;
      }
      if (saved.test.code === 'no_product_access') {
        setStatus(testCodeMessage('no_product_access', ctx, replaceMode));
      } else {
        setStatus(krogerAppCopy.s6Ok);
      }
      setSavedOk(true);
      markDone(6);
      goStep(7);
      clearKrogerAppSetupProgress(householdId);
      onSaved(saved);
    } catch (requestError) {
      setError(inlineFailure(requestError, putFailureMessage(requestError)) ?? putFailureMessage(requestError));
    } finally {
      setTesting(false);
    }
  };

  const connect = () => {
    const authReturn = returnTo.startsWith('link:') ? 'shopping' : (returnTo === 'household' ? 'household' : 'shopping');
    void startKrogerConnect(authReturn);
  };

  const stepTitle = useMemo(() => {
    switch (step) {
      case 1: return krogerAppCopy.s1Title;
      case 2: return krogerAppCopy.s2Title;
      case 3: return krogerAppCopy.s3Title;
      case 4: return krogerAppCopy.s4Title;
      case 5: return krogerAppCopy.s5Title;
      case 6: return krogerAppCopy.s6Title;
      case 7: return krogerAppCopy.s7Title;
      default: return krogerAppCopy.title;
    }
  }, [step]);

  const s1Body = ctx.store === 'your store' ? krogerAppCopy.s1BodyFallback : krogerAppCopy.s1Body.replaceAll('{store}', ctx.store);
  const s7Body = ctx.store === 'your store' ? krogerAppCopy.s7BodyFallback : krogerAppCopy.s7Body.replaceAll('{store}', ctx.store);
  const connectLabel = ctx.store === 'your store'
    ? krogerAppCopy.s7ConnectFallback
    : krogerAppCopy.s7Connect.replaceAll('{store}', ctx.store);

  return (
    <>
      <Modal
        className="store-app-setup-modal"
        opened={opened}
        onClose={requestClose}
        title={replaceMode ? krogerAppCopy.menuReplace : krogerAppCopy.title}
        size="lg"
        closeButtonProps={{ 'aria-label': krogerAppCopy.closeLabel }}
      >
        <Stack gap="sm">
          <Text size="sm" c="dimmed">{krogerAppCopy.progress(step)}</Text>
          {resumeNotice !== '' && <Alert variant="light">{resumeNotice}</Alert>}
          {replaceMode && step >= 6 && (
            <Text size="sm">{krogerAppCopy.replaceNote}</Text>
          )}
          <Button variant="subtle" size="compact-sm" onClick={() => setStepsOpen((v) => !v)}>
            {krogerAppCopy.stepsDisclosure}
          </Button>
          <Collapse expanded={stepsOpen}>
            <div className="store-app-step-nav">
              {Array.from({ length: TOTAL_STEPS }, (_, i) => i + 1).map((n) => (
                <Button
                  key={n}
                  size="compact-xs"
                  variant={n === step ? 'filled' : 'default'}
                  className="store-app-touch-target"
                  onClick={() => goStep(n)}
                >
                  {done.includes(n) ? krogerAppCopy.stepDone : krogerAppCopy.goStep(n)}
                </Button>
              ))}
            </div>
          </Collapse>

          <div className="store-app-setup-body">
            <Title order={4}>{stepTitle}</Title>

            {step === 1 && (
              <Stack gap="sm" mt="sm">
                <Text size="sm">{s1Body}</Text>
                <Text size="sm">{krogerLabels.s1Account}</Text>
                <Anchor href={krogerLabels.urlHome} target="_blank" rel="noopener noreferrer">
                  {krogerAppCopy.s1Open}
                </Anchor>
                <Text size="sm">{krogerAppCopy.s1After}</Text>
              </Stack>
            )}

            {step === 2 && (
              <Stack gap="sm" mt="sm">
                <Text size="sm">{krogerLabels.s2Body}</Text>
                <Alert color="yellow" variant="light">{krogerAppCopy.s2Warn}</Alert>
              </Stack>
            )}

            {step === 3 && (
              <Stack gap="sm" mt="sm">
                {noAddress ? (
                  <Alert color="red" variant="light" role="alert">{krogerAppCopy.errNoAddress}</Alert>
                ) : (
                  <>
                    <Text size="sm">{krogerAppCopy.s3Body}</Text>
                    <Text size="sm">{krogerAppCopy.s3Exact}</Text>
                    <KrogerAppCopyRow
                      label={krogerAppCopy.rowLabelName}
                      value={suggestedName}
                      copyLabel={krogerAppCopy.copyName}
                    />
                    <Text size="xs" c="dimmed">{krogerAppCopy.s3NameTaken}</Text>
                    <KrogerAppCopyRow
                      label={krogerAppCopy.rowLabelDesc}
                      value={krogerAppCopy.s3DescValue}
                      copyLabel={krogerAppCopy.copyDesc}
                    />
                    <KrogerAppCopyRow
                      label={krogerAppCopy.rowLabelAddress}
                      value={redirectUri}
                      copyLabel={krogerAppCopy.copyAddress}
                    />
                    <Text size="xs" c="dimmed">
                      {krogerAppCopy.krogerCallsRedirect(krogerLabels.fRedirect)}
                    </Text>
                    <Text size="sm">{krogerAppCopy.s3Email}</Text>
                  </>
                )}
              </Stack>
            )}

            {step === 4 && (
              <Stack gap="sm" mt="sm">
                <Text size="sm">{krogerLabels.s4Body}</Text>
                <Alert color="yellow" variant="light">{krogerAppCopy.s4Warn}</Alert>
                <ul className="store-app-checklist">
                  <li>{krogerLabels.boxCart}</li>
                  <li>{krogerLabels.boxProducts}</li>
                  <li>{krogerLabels.boxLocations} ({krogerAppCopy.s4IfSeen})</li>
                </ul>
              </Stack>
            )}

            {step === 5 && (
              <Stack gap="sm" mt="sm">
                <Text size="sm">{krogerLabels.s5Body}</Text>
                <Alert variant="light">{krogerAppCopy.s5Once}</Alert>
              </Stack>
            )}

            {step === 6 && (
              <Stack gap="sm" mt="sm">
                <Text size="sm">{krogerAppCopy.s6Body}</Text>
                <TextInput
                  label={krogerLabels.fId}
                  description={krogerLabels.fIdHint}
                  value={clientId}
                  onChange={(event) => {
                    typedSecrets.current = true;
                    setClientId(event.currentTarget.value);
                  }}
                />
                <PasswordInput
                  label={krogerLabels.fSecret}
                  description={krogerLabels.fSecretHint}
                  visible={showSecret}
                  onVisibilityChange={setShowSecret}
                  visibilityToggleButtonProps={{
                    'aria-label': showSecret ? krogerAppCopy.s6Hide : krogerAppCopy.s6Show,
                  }}
                  value={clientSecret}
                  onChange={(event) => {
                    typedSecrets.current = true;
                    setClientSecret(event.currentTarget.value);
                  }}
                />
                <NativeSelect
                  label="Modality"
                  value={modality}
                  data={[
                    { value: 'PICKUP', label: 'Pickup' },
                    { value: 'DELIVERY', label: 'Delivery' },
                  ]}
                  onChange={(event) => setModality(event.currentTarget.value)}
                />
                {fieldError !== '' && <Text role="alert" c="red" size="sm">{fieldError}</Text>}
                {error !== '' && <Text role="alert" c="red" size="sm">{error}</Text>}
                {status !== '' && <Text role="status" c="teal" size="sm">{status}</Text>}
              </Stack>
            )}

            {step === 7 && (
              <Stack gap="sm" mt="sm">
                <Text size="sm">{s7Body}</Text>
                <Text size="sm">{krogerAppCopy.s7Hint}</Text>
              </Stack>
            )}
          </div>

          <div className="store-app-setup-footer">
            <Group justify="space-between" wrap="wrap">
              {step > 1 && step < 7 ? (
                <Button className="store-app-touch-target" variant="default" onClick={() => goStep(step - 1)}>
                  {krogerAppCopy.back}
                </Button>
              ) : <span />}
              <Group gap="sm">
                {replaceMode && step >= 6 && (
                  <Button className="store-app-touch-target" variant="default" onClick={onClose}>
                    {krogerAppCopy.replaceCancel}
                  </Button>
                )}
                {step < 6 && (
                  <Button
                    className="store-app-touch-target"
                    disabled={step === 3 && noAddress}
                    onClick={() => {
                      markDone(step);
                      goStep(step + 1);
                    }}
                  >
                    {krogerAppCopy.next}
                  </Button>
                )}
                {step === 6 && (
                  <Button
                    className="store-app-touch-target"
                    loading={testing}
                    onClick={() => { void testAndSave(); }}
                  >
                    {testing ? krogerAppCopy.s6Testing : krogerAppCopy.s6Test}
                  </Button>
                )}
                {step === 7 && savedOk && (
                  <Button className="store-app-touch-target" onClick={connect}>
                    {connectLabel}
                  </Button>
                )}
              </Group>
            </Group>
          </div>
        </Stack>
      </Modal>

      <Modal opened={closeConfirm} onClose={() => setCloseConfirm(false)} title={krogerAppCopy.closeConfirmTitle} size="sm">
        <Stack gap="md">
          <Text size="sm">{krogerAppCopy.closeConfirmBody}</Text>
          <Group justify="flex-end">
            <Button variant="default" className="store-app-touch-target" onClick={() => setCloseConfirm(false)}>
              {krogerAppCopy.closeConfirmKeep}
            </Button>
            <Button className="store-app-touch-target" onClick={confirmClose}>
              {krogerAppCopy.closeConfirmClose}
            </Button>
          </Group>
        </Stack>
      </Modal>
    </>
  );
}
