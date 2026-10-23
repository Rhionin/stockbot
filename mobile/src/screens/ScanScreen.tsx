import { useCallback, useEffect, useRef, useState } from 'react';
import { ActivityIndicator, Linking, Pressable, StyleSheet, Text, View } from 'react-native';
import { CameraView, useCameraPermissions } from 'expo-camera';
import * as Haptics from 'expo-haptics';
import { StatusBar } from 'expo-status-bar';
import { useSafeAreaInsets } from 'react-native-safe-area-context';
import type { ScanDirection, ScannerConfig } from '../../../frontend/src/types';
import type { PriorScan } from '../../../frontend/src/components/scanner/scanTiming';
import { inlineFailure } from '../../../frontend/src/api/contract';
import type { StockbotClient } from '../api/client';
import { ServerNoticeBanner } from '../ui/ServerNoticeBanner';
import { CAMERA_BARCODE_TYPES } from '../scanner/formats';
import { decideScan } from '../scanner/decideScan';
import { modeLabel } from '../scanner/modeLabel';
import { colors, fonts } from '../ui/theme';

export interface ScanScreenProps {
  client: StockbotClient;
  warning: string;
}

export function ScanScreen({ client, warning }: ScanScreenProps) {
  const insets = useSafeAreaInsets();
  const [permission, requestPermission] = useCameraPermissions();
  const [config, setConfig] = useState<ScannerConfig | null>(null);
  const [mode, setMode] = useState<ScanDirection>('stock_in');
  const [setupError, setSetupError] = useState('');
  const [notice, setNotice] = useState('Point at a barcode');
  const configRef = useRef<ScannerConfig | null>(null);
  const modeRef = useRef<ScanDirection>('stock_in');
  const lastRef = useRef<PriorScan | null>(null);
  const clientRef = useRef(client);
  const [reloadKey, setReloadKey] = useState(0);

  useEffect(() => {
    clientRef.current = client;
  }, [client]);

  useEffect(() => {
    let active = true;
    client.getScannerConfig().then((next) => {
      if (!active) return;
      configRef.current = next;
      modeRef.current = next.currentMode;
      setConfig(next);
      setMode(next.currentMode);
      setSetupError('');
    }).catch((error: unknown) => {
      if (!active) return;
      configRef.current = null;
      setConfig(null);
      setSetupError(inlineFailure(error, 'The scanner setup did not load.') ?? '');
    });
    return () => {
      active = false;
    };
  }, [client, reloadKey]);

  async function switchMode(next: ScanDirection) {
    if (next === modeRef.current) return;
    modeRef.current = next;
    setMode(next);
    try {
      const response = await client.setScannerMode(next);
      modeRef.current = response.mode;
      setMode(response.mode);
      setNotice(response.mode === 'stock_in' ? 'Switched to putting away' : 'Switched to using up');
    } catch (error) {
      setNotice(inlineFailure(error, 'The scan mode did not change.') ?? '');
      setReloadKey((value) => value + 1);
    }
  }

  const onBarcodeScanned = useCallback((result: { data: string }) => {
    const decision = decideScan(result.data, configRef.current, lastRef.current, Date.now());
    if (decision.kind === 'ignore' || decision.kind === 'wait') return;
    const acceptedAt = Date.now();
    lastRef.current = { value: decision.barcode, at: acceptedAt };
    void Haptics.notificationAsync(Haptics.NotificationFeedbackType.Success);
    void (async () => {
      try {
        if (decision.kind === 'mode') {
          const response = await clientRef.current.setScannerMode(decision.mode);
          modeRef.current = response.mode;
          setMode(response.mode);
          setNotice(response.mode === 'stock_in' ? 'Switched to putting away' : 'Switched to using up');
          return;
        }
        const entry = await clientRef.current.createScan(decision.barcode, modeRef.current);
        if (entry.status === 'flagged' || entry.product === null) {
          setNotice(`${entry.barcode} needs a name on the pantry site`);
          return;
        }
        const name = entry.product.name || entry.barcode;
        setNotice(entry.direction === 'stock_out' ? `Used ${name}` : `Added ${name}`);
      } catch (error) {
        if (lastRef.current?.at === acceptedAt) lastRef.current = null;
        void Haptics.notificationAsync(Haptics.NotificationFeedbackType.Error);
        setNotice(inlineFailure(error, 'The scan did not save.') ?? '');
      }
    })();
  }, []);

  const cameraReady = permission?.granted === true && config !== null;

  return (
    <View style={styles.screen}>
      <StatusBar style="light" />
      <View style={[styles.band, { paddingTop: insets.top + 12 }]}>
        <ServerNoticeBanner />
        <View style={styles.modes}>
          <ModeWord mode="stock_in" current={mode} onPress={switchMode} />
          <ModeWord mode="stock_out" current={mode} onPress={switchMode} />
        </View>
        {warning !== '' && <Text style={styles.warning}>{warning}</Text>}
      </View>

      <View style={styles.stage}>
        {cameraReady ? (
          <CameraView
            style={StyleSheet.absoluteFill}
            facing="back"
            barcodeScannerSettings={{ barcodeTypes: CAMERA_BARCODE_TYPES }}
            onBarcodeScanned={onBarcodeScanned}
          />
        ) : (
          <View style={styles.hold}>
            {permission === null || (permission.granted && config === null && setupError === '') ? (
              <>
                <ActivityIndicator color={colors.white} />
                <Text style={styles.holdText}>
                  {permission === null ? 'Checking the camera…' : 'Reading the scanner setup…'}
                </Text>
              </>
            ) : setupError !== '' ? (
              <>
                <Text style={styles.holdText}>{setupError}</Text>
                <Pressable accessibilityRole="button" accessibilityLabel="Try again" onPress={() => { setReloadKey((value) => value + 1); }} style={styles.allow}>
                  <Text style={styles.allowText}>Try again</Text>
                </Pressable>
              </>
            ) : permission?.canAskAgain === false ? (
              <>
                <Text style={styles.holdText}>Camera access is off. Allow it in Settings, then come back.</Text>
                <Pressable accessibilityRole="button" accessibilityLabel="Open settings" onPress={() => { void Linking.openSettings(); }} style={styles.allow}>
                  <Text style={styles.allowText}>Open settings</Text>
                </Pressable>
              </>
            ) : (
              <>
                <Text style={styles.holdText}>The camera reads the barcode on the package.</Text>
                <Pressable accessibilityRole="button" accessibilityLabel="Allow the camera" onPress={() => { void requestPermission(); }} style={styles.allow}>
                  <Text style={styles.allowText}>Allow the camera</Text>
                </Pressable>
              </>
            )}
          </View>
        )}
      </View>

      <View style={styles.footer}>
        <Text style={styles.notice} accessibilityLiveRegion="polite">{notice}</Text>
      </View>
    </View>
  );
}

function ModeWord({
  mode,
  current,
  onPress,
}: {
  mode: ScanDirection;
  current: ScanDirection;
  onPress: (mode: ScanDirection) => void;
}) {
  const active = mode === current;
  const label = modeLabel(mode);
  return (
    <Pressable
      accessibilityRole="button"
      accessibilityState={{ selected: active }}
      accessibilityLabel={active ? label : `Switch to ${label.toLowerCase()}`}
      onPress={() => onPress(mode)}
      style={styles.modeHit}
    >
      <Text style={[styles.mode, active ? styles.modeActive : styles.modeIdle]}>{label}</Text>
      <View style={[styles.underline, active ? { backgroundColor: mode === 'stock_in' ? colors.shelf : colors.using } : null]} />
    </Pressable>
  );
}

const styles = StyleSheet.create({
  screen: {
    flex: 1,
    backgroundColor: colors.camera,
  },
  band: {
    paddingHorizontal: 20,
    paddingBottom: 14,
    backgroundColor: colors.camera,
  },
  modes: {
    flexDirection: 'row',
    alignItems: 'flex-end',
    gap: 22,
  },
  modeHit: {
    minHeight: 48,
    justifyContent: 'flex-end',
  },
  mode: {
    fontFamily: fonts.bold,
    color: colors.white,
  },
  modeActive: {
    fontSize: 28,
    lineHeight: 34,
  },
  modeIdle: {
    fontSize: 18,
    lineHeight: 24,
    opacity: 0.55,
  },
  underline: {
    height: 4,
    marginTop: 6,
    backgroundColor: 'transparent',
  },
  warning: {
    fontFamily: fonts.regular,
    color: colors.white,
    fontSize: 15,
    lineHeight: 21,
    marginTop: 10,
  },
  stage: {
    flex: 1,
    backgroundColor: colors.camera,
  },
  hold: {
    flex: 1,
    alignItems: 'flex-start',
    justifyContent: 'center',
    paddingHorizontal: 24,
    gap: 16,
  },
  holdText: {
    fontFamily: fonts.regular,
    color: colors.white,
    fontSize: 18,
    lineHeight: 26,
    maxWidth: 340,
  },
  allow: {
    backgroundColor: colors.shelf,
    minHeight: 48,
    justifyContent: 'center',
    paddingHorizontal: 16,
  },
  allowText: {
    fontFamily: fonts.bold,
    color: colors.white,
    fontSize: 16,
  },
  footer: {
    paddingHorizontal: 20,
    paddingTop: 14,
    paddingBottom: 16,
    backgroundColor: colors.camera,
    minHeight: 72,
    justifyContent: 'center',
  },
  notice: {
    fontFamily: fonts.regular,
    color: colors.white,
    fontSize: 18,
    lineHeight: 24,
  },
});
