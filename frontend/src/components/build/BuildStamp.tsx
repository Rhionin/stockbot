import { useEffect, useState } from 'react';
import { Text } from '@mantine/core';
import { getBuildInfo } from '../../api/client';
import type { BuildInfo } from '@stockbot/shared/types';
import { formatBuildLabel } from '../../utils/buildLabel';

// BuildStamp shows which build is running. It sits in the menu's Build section
// so the id stays reachable without a bar on every page. The version, subject,
// and timestamp make a freshly deployed change recognizable; the commit matches
// the container tag.
export function BuildStamp() {
  const [info, setInfo] = useState<BuildInfo | null>(null);

  useEffect(() => {
    let cancelled = false;
    getBuildInfo()
      .then((buildInfo) => {
        if (!cancelled) setInfo(buildInfo);
      })
      .catch(() => {
        // A missing build identity should not interrupt the rest of the app.
      });
    return () => {
      cancelled = true;
    };
  }, []);

  if (info === null) return null;
  const label = formatBuildLabel(info);
  if (label === null) return null;

  return (
    <Text
      component="div"
      size="xs"
      c="dimmed"
      role="note"
      aria-label={label.accessibleName}
      title={label.title}
      ta="left"
      style={{ width: '100%', minWidth: 0, lineHeight: 1.3, overflowWrap: 'anywhere' }}
    >
      {label.subject !== '' ? <span style={{ display: 'block' }}>{label.subject}</span> : null}
      {label.version !== '' ? (
        <span style={{ display: 'block', fontFamily: 'var(--mono)' }}>{label.version}</span>
      ) : null}
      {label.detail !== '' ? (
        <span style={{ display: 'block', fontFamily: 'var(--mono)' }}>{label.detail}</span>
      ) : null}
    </Text>
  );
}
