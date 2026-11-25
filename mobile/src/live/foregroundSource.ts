export interface ForegroundSource {
  isActive: () => boolean;
  subscribe: (listener: (active: boolean) => void) => () => void;
}
