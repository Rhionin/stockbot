import { useState } from 'react';
import { Button } from '@mantine/core';
import { updateScanEntry } from '../../api/client';
import type { ScanEntry } from '@stockbot/shared/types';
import { qrPreview } from '@stockbot/shared/qrPreview';
import { inlineFailure } from '@stockbot/shared/contract';
import { QR_CARD_TITLE, QR_DISMISS_FAILURE, QR_DISMISS_LABEL, QR_RESCAN_LABEL, qrCardLabel } from '@stockbot/shared/queueCards';
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

  const dismiss = async () => {
    setDismissing(true);
    setError('');
    try {
      await updateScanEntry(entry.id, { status: 'cancelled' });
      onChanged();
    } catch (requestError) {
      setError(inlineFailure(requestError, QR_DISMISS_FAILURE) ?? '');
    } finally {
      setDismissing(false);
    }
  };

  return (
    <article
      className={justCaptured ? 'qr-scan qr-scan--just-captured' : 'qr-scan'}
      aria-label={qrCardLabel(preview)}
    >
      <div className="qr-scan-rail" aria-hidden="true">
        <span /><span /><span /><span /><span /><span /><span />
      </div>
      <div className="qr-scan-copy">
        <p className="qr-scan-title">{QR_CARD_TITLE}</p>
        {preview !== '' && <p className="qr-scan-payload">{preview}</p>}
      </div>
      <div className="qr-scan-actions">
        <Button className="qr-scan-rescan" color="indigo" size="sm" onClick={onRescan}>
          {QR_RESCAN_LABEL}
        </Button>
        <Button
          className="qr-scan-dismiss"
          variant="default"
          size="sm"
          loading={dismissing}
          onClick={() => void dismiss()}
        >
          {QR_DISMISS_LABEL}
        </Button>
        {error !== '' && <p className="qr-scan-error" role="alert">{error}</p>}
      </div>
    </article>
  );
};
