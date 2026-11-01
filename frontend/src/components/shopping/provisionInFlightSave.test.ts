import { describe, expect, it, vi } from 'vitest';
import { dedupeConcurrentSave } from './provisionInFlightSave';

describe('dedupeConcurrentSave', () => {
  it('returns one in-flight promise when start is invoked twice for the same entry', async () => {
    const inFlight: Record<string, Promise<boolean>> = {};
    let startCalls = 0;
    let release!: () => void;
    const start = vi.fn(() => {
      startCalls += 1;
      return new Promise<boolean>((resolve) => {
        release = () => resolve(true);
      });
    });
    const first = dedupeConcurrentSave(inFlight, 'e1', start);
    const second = dedupeConcurrentSave(inFlight, 'e1', start);
    expect(startCalls).toBe(1);
    expect(second).toBe(first);
    release();
    await first;
  });
});
