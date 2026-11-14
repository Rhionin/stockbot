import { useState } from 'react';
import { productInitials } from './photoInitials';

function PackageIcon() {
  return (
    <svg viewBox="0 0 24 24" width="22" height="22" aria-hidden="true">
      <path
        fill="currentColor"
        d="M12 2.5 3.5 7v10L12 21.5 20.5 17V7L12 2.5zm0 2.3 6.2 3.2L12 11.2 5.8 7.9 12 4.8zM5.5 9.4l5.5 2.9v6.4l-5.5-2.8V9.4zm13 0v6.5l-5.5 2.8v-6.4l5.5-2.9z"
      />
    </svg>
  );
}

export function ProductPhoto({
  src,
  name,
  stacked = false,
  decorative = false,
  showSkeleton = false,
  initialsFallback = false,
  onImageFailed,
}: {
  src?: string;
  name: string;
  stacked?: boolean;
  decorative?: boolean;
  showSkeleton?: boolean;
  initialsFallback?: boolean;
  onImageFailed?: () => void;
}) {
  const [failedFor, setFailedFor] = useState<string | null>(null);
  const [loadedFor, setLoadedFor] = useState<string | null>(null);
  const trimmed = src?.trim() ?? '';
  const failed = failedFor === trimmed;
  const loaded = loadedFor === trimmed;
  const show = trimmed !== '' && !failed;

  const initials = productInitials(name);
  const fallbackContent = initialsFallback && initials !== '' ? (
    <span className="shelf-photo-initials">{initials}</span>
  ) : (
    <PackageIcon />
  );

  return (
    <span className={stacked ? 'shelf-photo is-stack' : 'shelf-photo'}>
      {show ? (
        <>
          {showSkeleton && !loaded && (
            <span className="shelf-photo-skeleton" aria-hidden="true" />
          )}
          <img
            src={trimmed}
            alt={decorative ? '' : name}
            loading="lazy"
            onLoad={() => setLoadedFor(trimmed)}
            onError={() => {
              setFailedFor(trimmed);
              onImageFailed?.();
            }}
            className={showSkeleton && !loaded ? 'is-loading' : undefined}
          />
        </>
      ) : decorative ? (
        <span className="shelf-photo-fallback" aria-hidden="true">
          {fallbackContent}
        </span>
      ) : (
        <span className="shelf-photo-fallback" role="img" aria-label={`No photo of ${name}`}>
          {fallbackContent}
        </span>
      )}
    </span>
  );
}
