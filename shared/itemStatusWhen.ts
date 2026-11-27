function sameCalendarDay(left: Date, right: Date): boolean {
  return left.getFullYear() === right.getFullYear()
    && left.getMonth() === right.getMonth()
    && left.getDate() === right.getDate();
}

/** Relative when for item / off-list status (today / yesterday / Tue / Nov 3). */
export function formatItemStatusRelativeWhen(iso: string, now: Date = new Date()): string {
  const at = new Date(iso);
  if (Number.isNaN(at.getTime())) return iso;
  if (sameCalendarDay(at, now)) return 'today';
  const yesterday = new Date(now);
  yesterday.setDate(now.getDate() - 1);
  if (sameCalendarDay(at, yesterday)) return 'yesterday';
  const msPerDay = 86_400_000;
  const diffDays = Math.floor((now.getTime() - at.getTime()) / msPerDay);
  if (diffDays >= 0 && diffDays < 7) {
    return new Intl.DateTimeFormat('en-US', { weekday: 'short' }).format(at);
  }
  if (at.getFullYear() === now.getFullYear()) {
    return new Intl.DateTimeFormat('en-US', { month: 'short', day: 'numeric' }).format(at);
  }
  return new Intl.DateTimeFormat('en-US', { month: 'short', day: 'numeric', year: 'numeric' }).format(at);
}