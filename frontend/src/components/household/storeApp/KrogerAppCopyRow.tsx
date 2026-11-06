import { useState } from 'react';
import { Button, Stack, Text } from '@mantine/core';
import { krogerAppCopy } from '@stockbot/shared/krogerAppCopy';

export function KrogerAppCopyRow({
  label,
  value,
  copyLabel,
  copiedLabel,
  onCopied,
}: {
  label: string;
  value: string;
  copyLabel: string;
  copiedLabel?: string;
  onCopied?: (what: string) => void;
}) {
  const [copied, setCopied] = useState(false);

  const copy = async () => {
    try {
      await navigator.clipboard.writeText(value);
      setCopied(true);
      onCopied?.(label);
      window.setTimeout(() => setCopied(false), 2000);
    } catch {
      setCopied(false);
    }
  };

  return (
    <div className="store-app-copy-row">
      <Stack gap={2}>
        <Text size="sm" fw={500}>{label}</Text>
        <Text className="store-app-copy-value" size="sm">{value}</Text>
      </Stack>
      <Button
        className="store-app-touch-target"
        variant="default"
        size="compact-sm"
        aria-label={copyLabel}
        onClick={() => { void copy(); }}
      >
        {copied ? (copiedLabel ?? krogerAppCopy.copyCopied) : krogerAppCopy.copy}
      </Button>
    </div>
  );
}
