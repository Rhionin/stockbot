// Quiet copy for the running build. The commit is the image tag suffix
// (ghcr.io/rhionin/pantry:<commit>). The version, subject, and timestamp tell
// which change is deployed without opening GitHub.
export interface BuildIdentity {
  commit: string;
  committedAt?: string;
  version?: string;
  subject?: string;
}

export interface BuildLabel {
  subject: string;
  version: string;
  detail: string;
  accessibleName: string;
  title: string;
}

const ISO_TIME = /^(\d{4}-\d{2}-\d{2})[T ](\d{2}:\d{2})(?::\d{2}(?:\.\d+)?)?(Z|[+-]\d{2}:\d{2})?$/;

// formatCommitTime renders an ISO 8601 commit timestamp as a short UTC (or
// offset) time. Empty input stays empty. A value that is not ISO 8601 is
// returned unchanged so an unusual stamp is still visible.
export function formatCommitTime(value: string): string {
  const trimmed = value.trim();
  if (trimmed === '') return '';
  const match = ISO_TIME.exec(trimmed);
  if (!match) return trimmed;
  const zone = match[3];
  const zoneLabel = zone === undefined || zone === 'Z' || zone === '+00:00' ? 'UTC' : zone;
  return `${match[1]} ${match[2]} ${zoneLabel}`;
}

function collapseWhitespace(value: string): string {
  return value.replace(/\s+/g, ' ').trim();
}

// formatBuildLabel turns a build identity into menu copy. An identity with
// no commit, time, version, or subject renders nothing. Empty time and
// subject are left out rather than shown as blanks.
export function formatBuildLabel(info: BuildIdentity): BuildLabel | null {
  const commit = info.commit ?? '';
  const subject = collapseWhitespace(info.subject ?? '');
  const version = collapseWhitespace(info.version ?? '');
  const time = formatCommitTime(info.committedAt ?? '');
  if (commit === '' && subject === '' && time === '' && version === '') return null;

  const detail = commit !== '' && time === ''
    ? `build ${commit}`
    : commit !== ''
      ? `${time} · ${commit}`
      : time;

  const nameParts = [];
  if (subject !== '') nameParts.push(subject);
  if (time !== '') nameParts.push(time);
  if (commit !== '') nameParts.push(`build ${commit}`);
  if (version !== '') nameParts.push(`version ${version}`);

  const accessibleName = nameParts.join(', ');
  return {
    subject,
    version,
    detail,
    accessibleName,
    title: accessibleName,
  };
}
