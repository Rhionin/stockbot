import type { ConfigContext, ExpoConfig } from 'expo/config';
import { withEasUpdates } from './eas-config';

// TODO(CJ): `owner` and `extra.eas.projectId` are intentionally unset.
// Choose the Expo account that should hold this project and its Android
// signing key, then from mobile/ run `eas init`. That writes `owner` and
// `extra.eas.projectId` into app.json. Do not guess them. Once the project
// id is there, this file sets `updates.url` to https://u.expo.dev/<projectId>
// and turns EAS Update on. Until then updates stay disabled.

export default ({ config }: ConfigContext): ExpoConfig => withEasUpdates(config);
