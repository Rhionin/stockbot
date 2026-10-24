// Camera capture is opt-in and only available in a secure context. Browsers
// refuse getUserMedia on plain HTTP (a phone hitting a home server by IP), so
// the page explains that instead of prompting.

export { RESCAN_COOLDOWN_MS, shouldAcceptScan, type PriorScan } from '@stockbot/shared/scanTiming';

export const SCAN_INTERVAL_MS = 250;
export const CAMERA_START_TIMEOUT_MS = 8000;

export type FacingMode = 'environment' | 'user';

export type CameraBlockKind = 'insecure' | 'unavailable' | 'unsupported' | 'camera';

export interface CameraBlock {
  kind: CameraBlockKind;
  message: string;
}

const HTTPS_MESSAGE = 'Camera scanning needs a secure connection (HTTPS). Open this page over HTTPS, or type the barcode instead.';

export function cameraBlockReason(): CameraBlock | null {
  if (typeof window === 'undefined' || !window.isSecureContext) {
    return { kind: 'insecure', message: HTTPS_MESSAGE };
  }
  if (typeof navigator.mediaDevices?.getUserMedia !== 'function') {
    return {
      kind: 'unavailable',
      message: 'This browser cannot access a camera. Type the barcode instead.',
    };
  }
  return null;
}

export function describeCameraError(error: unknown): string {
  const name = error instanceof DOMException ? error.name : '';
  switch (name) {
    case 'NotAllowedError':
    case 'PermissionDeniedError':
      return 'Camera access was denied. Allow camera permission for this site, then try again.';
    case 'NotFoundError':
    case 'DevicesNotFoundError':
      return 'No camera was found on this device.';
    case 'NotReadableError':
    case 'TrackStartError':
      return 'The camera is already in use by another app.';
    case 'SecurityError':
      return HTTPS_MESSAGE;
    case 'AbortError':
      return 'The camera did not respond. Try again.';
    default:
      return 'Camera access was denied or is unavailable.';
  }
}

function isHardCameraFailure(error: unknown): boolean {
  if (!(error instanceof DOMException)) return false;
  return error.name === 'NotAllowedError'
    || error.name === 'PermissionDeniedError'
    || error.name === 'SecurityError';
}

// Prefer the rear camera on a phone, but accept the only camera a laptop has
// when the facing-mode hint cannot be satisfied.
export async function openCameraStream(facingMode: FacingMode): Promise<MediaStream> {
  const preferred: MediaStreamConstraints = {
    video: { facingMode: { ideal: facingMode } },
    audio: false,
  };
  try {
    return await navigator.mediaDevices.getUserMedia(preferred);
  } catch (error) {
    if (isHardCameraFailure(error)) throw error;
    return navigator.mediaDevices.getUserMedia({ video: true, audio: false });
  }
}
