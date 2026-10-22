import * as Application from 'expo-application';
import Constants from 'expo-constants';
import * as Updates from 'expo-updates';
import { StyleSheet, Text, View } from 'react-native';
import { colors, fonts } from '../ui/theme';
import { describeBuild } from './buildStamp';

export function BuildStamp() {
  const stamp = describeBuild({
    nativeApplicationVersion: Application.nativeApplicationVersion,
    nativeBuildVersion: Application.nativeBuildVersion,
    configVersion: Constants.expoConfig?.version,
    updateId: Updates.updateId,
    runtimeVersion: Updates.runtimeVersion,
    updatesEnabled: Updates.isEnabled,
  });

  return (
    <View
      accessibilityLabel={`Version ${stamp.version}. Update ${stamp.updateId}. Runtime ${stamp.runtimeVersion}.`}
      style={styles.block}
    >
      <Text selectable style={styles.line}>Version {stamp.version}</Text>
      <Text selectable style={styles.line}>Update {stamp.updateId}</Text>
      <Text selectable style={styles.line}>Runtime {stamp.runtimeVersion}</Text>
    </View>
  );
}

const styles = StyleSheet.create({
  block: {
    marginTop: 28,
    maxWidth: 480,
  },
  line: {
    fontFamily: fonts.regular,
    fontSize: 14,
    lineHeight: 20,
    color: colors.inkMuted,
  },
});
