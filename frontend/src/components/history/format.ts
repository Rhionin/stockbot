export function formatPaceNumber(days: number): string {
  const rounded = Math.round(days * 10) / 10;
  return Number.isInteger(rounded) ? String(rounded) : rounded.toFixed(1);
}

export function onHandLabel(count: number): string {
  if (count === 0) return 'None on hand';
  if (count === 1) return '1 on hand';
  return `${count} on hand`;
}

export function daysLeftLabel(days: number): string {
  if (days === 1) return 'about 1 day left';
  return `about ${days} days left`;
}

export function trendLabel(trend: string | undefined): string {
  if (trend === 'steady') return 'Steady compared with the previous 30 days';
  if (trend === 'faster') return 'Faster than the previous 30 days';
  if (trend === 'slower') return 'Slower than the previous 30 days';
  return '';
}

export function moveTitle(direction: string, quantity: number): string {
  if (direction === 'in') return `Stocked in ${quantity}`;
  return `Used ${quantity}`;
}

export function sourceLabel(source: string): string {
  if (source === 'backfill') return 'Added when taking out';
  return source === 'scan' ? 'scan' : 'by hand';
}

function sameDay(left: Date, right: Date): boolean {
  return left.getFullYear() === right.getFullYear()
    && left.getMonth() === right.getMonth()
    && left.getDate() === right.getDate();
}

// formatMoveWhen names the calendar day when it is today or yesterday, and
// includes the weekday and date otherwise so "Wed" is not an earlier week.
export function formatMoveWhen(at: Date, now: Date, source: string): string {
  const time = new Intl.DateTimeFormat(undefined, { hour: 'numeric', minute: '2-digit' }).format(at);
  const how = sourceLabel(source);
  if (sameDay(at, now)) return `Today, ${time}, ${how}`;
  const yesterday = new Date(now);
  yesterday.setDate(now.getDate() - 1);
  if (sameDay(at, yesterday)) return `Yesterday, ${time}, ${how}`;
  const date = new Intl.DateTimeFormat(undefined, {
    weekday: 'short', month: 'short', day: 'numeric',
  }).format(at);
  return `${date}, ${time}, ${how}`;
}

export function formatMoveClock(at: Date, source: string): string {
  const time = new Intl.DateTimeFormat(undefined, { hour: 'numeric', minute: '2-digit' }).format(at);
  return `${time}, ${sourceLabel(source)}`;
}

export function dayHeading(at: Date, now: Date): string {
  if (sameDay(at, now)) return 'Today';
  const yesterday = new Date(now);
  yesterday.setDate(now.getDate() - 1);
  if (sameDay(at, yesterday)) return 'Yesterday';
  const options: Intl.DateTimeFormatOptions = {
    weekday: 'long',
    month: 'long',
    day: 'numeric',
  };
  if (at.getFullYear() !== now.getFullYear()) options.year = 'numeric';
  return new Intl.DateTimeFormat(undefined, options).format(at);
}

export interface DayGroup<T> {
  key: string;
  heading: string;
  moves: T[];
}

// ledgerDayKey matches a chart day to its section in the list.
export function ledgerDayKey(at: Date): string {
  return `${at.getFullYear()}-${at.getMonth()}-${at.getDate()}`;
}

// groupMovesByDay keeps the incoming order, which is newest first, and
// starts a new day only when the calendar day changes.
export function groupMovesByDay<T extends { at: string }>(moves: T[], now: Date): DayGroup<T>[] {
  const groups: DayGroup<T>[] = [];
  for (const move of moves) {
    const at = new Date(move.at);
    const key = ledgerDayKey(at);
    const last = groups[groups.length - 1];
    if (last && last.key === key) {
      last.moves.push(move);
    } else {
      groups.push({ key, heading: dayHeading(at, now), moves: [move] });
    }
  }
  return groups;
}

export function directionWord(direction: string): string {
  return direction === 'in' ? 'Stocked in' : 'Used';
}

export function ledgerEmptyCopy(direction: 'all' | 'in' | 'out', query: string): string {
  const searching = query.trim() !== '';
  if (!searching && direction === 'all') {
    return 'No stock moves yet. Stock something in or use something, and it shows up here.';
  }
  if (searching && direction === 'all') return 'No moves match that search.';
  if (searching && direction === 'in') return 'No stocked-in moves match that search.';
  if (searching && direction === 'out') return 'No uses match that search.';
  if (direction === 'in') return 'No stocked-in moves in this list.';
  return 'No uses in this list.';
}

export function deltaText(direction: string, quantity: number): string {
  return direction === 'in' ? `+${quantity}` : `−${quantity}`;
}

export function deltaName(direction: string, quantity: number): string {
  if (direction === 'in') {
    return quantity === 1 ? '1 stocked in' : `${quantity} stocked in`;
  }
  return quantity === 1 ? '1 used' : `${quantity} used`;
}

export function undoCopy(
  direction: string,
  quantity: number,
): { title: string; body: string; confirm: string } {
  if (direction === 'in') {
    const shelf = quantity === 1 ? '1 unit leaves the shelf' : `${quantity} units leave the shelf`;
    return {
      title: 'Undo this stock in?',
      body: `${shelf}. The pace is recalculated from the remaining history.`,
      confirm: 'Undo stock in',
    };
  }
  const shelf = quantity === 1 ? '1 unit goes back on the shelf' : `${quantity} units go back on the shelf`;
  return {
    title: 'Undo this use?',
    body: `${shelf}. The pace is recalculated from the remaining history.`,
    confirm: 'Undo this use',
  };
}

export function quantityPrompt(direction: string): string {
  return direction === 'in'
    ? 'How many units should this stock in be?'
    : 'How many units were actually used?';
}
