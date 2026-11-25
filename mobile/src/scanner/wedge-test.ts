import { WEDGE_MAX_GAP_MS, WEDGE_MAX_LENGTH, createWedgeReader, type WedgeKeyEvent } from './wedge';

const page = { tagName: 'BODY' };
const ownField = { tagName: 'INPUT' };

function press(key: string, target: unknown = page, extra: Partial<WedgeKeyEvent> = {}) {
  const event = { key, target, preventDefault: jest.fn(), stopPropagation: jest.fn(), ...extra };
  return event;
}

function setup() {
  let clock = 1_000;
  const scans: string[] = [];
  const reader = createWedgeReader((barcode) => scans.push(barcode), () => ownField, () => clock);
  const type = (text: string, gap: number, target: unknown = page) => {
    for (const char of text) {
      clock += gap;
      reader.handleKey(press(char, target));
    }
  };
  const enter = (gap: number, target: unknown = page) => {
    clock += gap;
    const event = press('Enter', target);
    reader.handleKey(event);
    return event;
  };
  const key = (name: string, gap: number, extra: Partial<WedgeKeyEvent> = {}) => {
    clock += gap;
    const event = press(name, page, extra);
    reader.handleKey(event);
    return event;
  };
  return { scans, type, enter, key, wait: (ms: number) => { clock += ms; } };
}

describe('keyboard-wedge reader', () => {
  it('counts fast keys followed by Enter as one scan and swallows the Enter', () => {
    const { scans, type, enter } = setup();

    type('0123456789012', 10);
    const event = enter(10);

    expect(scans).toEqual(['0123456789012']);
    expect(event.preventDefault).toHaveBeenCalled();
    expect(event.stopPropagation).toHaveBeenCalled();
  });

  it('reads two scans in a row', () => {
    const { scans, type, enter } = setup();

    type('4006381333931', 10);
    enter(10);
    type('STOCK_OUT', 10);
    enter(10);

    expect(scans).toEqual(['4006381333931', 'STOCK_OUT']);
  });

  it('does not count slow typing, and leaves its Enter alone', () => {
    const { scans, type, enter } = setup();

    type('0123456789012', WEDGE_MAX_GAP_MS + 50);
    const event = enter(WEDGE_MAX_GAP_MS + 50);

    expect(scans).toEqual([]);
    expect(event.preventDefault).not.toHaveBeenCalled();
  });

  it('starts over from the key that follows a pause', () => {
    const { scans, type, enter, wait } = setup();

    type('77', 10);
    wait(WEDGE_MAX_GAP_MS + 1);
    type('4006381333931', 10);
    enter(10);

    expect(scans).toEqual(['4006381333931']);
  });

  it('ignores an Enter that comes long after the last key', () => {
    const { scans, type, enter } = setup();

    type('4006381333931', 10);
    enter(WEDGE_MAX_GAP_MS + 1);

    expect(scans).toEqual([]);
  });

  it('ignores a bare Enter and a code too short to be a barcode', () => {
    const { scans, type, enter } = setup();

    expect(enter(10).preventDefault).not.toHaveBeenCalled();
    type('12', 10);
    expect(enter(10).preventDefault).not.toHaveBeenCalled();

    expect(scans).toEqual([]);
  });

  it('ignores typing in any other field, even a fast scan', () => {
    const { scans, type, enter } = setup();

    for (const target of [{ tagName: 'INPUT' }, { tagName: 'textarea' }, { tagName: 'SELECT' }, { tagName: 'DIV', isContentEditable: true }]) {
      if (target === ownField) continue;
      type('4006381333931', 5, { ...target });
      expect(enter(5, { ...target }).preventDefault).not.toHaveBeenCalled();
    }

    expect(scans).toEqual([]);
  });

  it('forgets keys typed before the person moved into a field', () => {
    const { scans, type, enter } = setup();

    type('4006', 5);
    type('x', 5, { tagName: 'INPUT' });
    type('381333931', 5);
    enter(5);

    expect(scans).toEqual(['381333931']);
  });

  it('leaves the dedicated scanner field to itself', () => {
    const { scans, type, enter } = setup();

    type('4006381333931', 5, ownField);
    enter(5, ownField);

    expect(scans).toEqual([]);
  });

  it('ignores shortcuts', () => {
    const { scans, type, key, enter } = setup();

    type('4006381333931', 5);
    const shortcut = key('v', 5, { ctrlKey: true });
    enter(5);

    expect(shortcut.preventDefault).not.toHaveBeenCalled();
    expect(scans).toEqual([]);
  });

  it('lets Shift ride inside a scan', () => {
    const { scans, type, key, enter } = setup();

    type('ST', 5);
    key('Shift', 5);
    type('OCK', 5);
    enter(5);

    expect(scans).toEqual(['STOCK']);
  });

  it('forgets a scan that a Tab or Escape interrupts', () => {
    const { scans, type, key, enter } = setup();

    type('4006381333931', 5);
    key('Escape', 5);
    enter(5);

    expect(scans).toEqual([]);
  });

  it('stops a runaway buffer, and its tail never becomes a scan', () => {
    const { scans, type, enter } = setup();

    type('1'.repeat(WEDGE_MAX_LENGTH + 5), 1);
    enter(1);

    expect(scans).toEqual([]);
  });

  it('keeps the keys of a scan in progress from pressing a focused button', () => {
    const { key } = setup();

    const first = key('A', 5);
    const second = key(' ', 5);

    expect(first.preventDefault).not.toHaveBeenCalled();
    expect(second.preventDefault).toHaveBeenCalled();
  });
});
