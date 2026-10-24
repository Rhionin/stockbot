import { useState } from 'react';
import { Button } from '@mantine/core';
import { updateScanEntry } from '../../api/client';
import type { ScanEntry } from '@stockbot/shared/types';
import { qrPreview } from './qrPreview';
import { inlineFailure } from '@stockbot/shared/contract';
import './qr-scan.css';

export interface QrScanCardProps {
  entry: ScanEntry;
  justCaptured?: boolean;
  onRescan: () => void;
  onChanged: () => void;
}

export const QrScanCard = ({
  entry,
  justCaptured = false,
  onRescan,
  onChanged,
}: QrScanCardProps) => {
  const [dismissing, setDismissing] = useState(false);
  const [error, setError] = useState('');
  const preview = qrPreview(entry.barcode);
  const label = preview === ''
    ? 'This was a QR code, not the barcode'
    : `This was a QR code, not the barcode. ${preview}`;

  const dismiss = async () => {
    setDismissing(true);
    setError('');
    try {
      await updateScanEntry(entry.id, { status: 'cancelled' });
      onChanged();
    } catch (requestError) {
      setError(inlineFailure(requestError, 'Couldn\'t dismiss that scan.') ?? '');
    } finally {
      setDismissing(false);
    }
  };

  return (
    <article
      className={justCaptured ? 'qr-scan qr-scan--just-captured' : 'qr-scan'}
      aria-label={label}
    >
      <div className="qr-scan-rail" aria-hidden="true">
        <span /><span /><span /><span /><span /><span /><span />
      </div>
      <div className="qr-scan-copy">
        <p className="qr-scan-title">This was a QR code, not the barcode</p>
        {preview !== '' && <p className="qr-scan-payload">{preview}</p>}
      </div>
      <div className="qr-scan-actions">
        <Button className="qr-scan-rescan" color="indigo" size="sm" onClick={onRescan}>
          Rescan the barcode
        </Button>
        <Button
          className="qr-scan-dismiss"
          variant="default"
          size="sm"
          loading={dismissing}
          onClick={() => void dismiss()}
        >
          Dismiss
        </Button>
        {error !== '' && <p className="qr-scan-error" role="alert">{error}</p>}
      </div>
    </article>
  );
};
