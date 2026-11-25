export function scrollCardIntoView(testId: string): void {
  document.querySelector(`[data-testid="${testId}"]`)?.scrollIntoView({ block: 'nearest' });
}
