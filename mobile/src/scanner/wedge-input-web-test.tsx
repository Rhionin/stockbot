import { View } from 'react-native';
import { act, render, screen } from '@testing-library/react-native';
import { focusScannerInput } from './focusScannerInput.web';
import { WedgeInput } from './WedgeInput.web';
import { WEDGE_INPUT_LABEL, WEDGE_INPUT_TEST_ID } from './wedgeInput';

type Handler = (event: unknown) => void;

const listeners = new Map<string, Handler>();
const focus = jest.fn();
const added = jest.fn();
const removed = jest.fn();

// Host refs in the test renderer are bare test instances. Give them the DOM's focus.
beforeAll(async () => {
  const probe = await render(<View />);
  Object.defineProperty(Object.getPrototypeOf(probe.container), 'focus', { value: focus, configurable: true });
});

beforeEach(() => {
  listeners.clear();
  focus.mockClear();
  added.mockClear();
  removed.mockClear();
  (globalThis as { document?: unknown }).document = {
    addEventListener: (type: string, handler: Handler, capture: boolean) => {
      added(type, capture);
      listeners.set(type, handler);
    },
    removeEventListener: (type: string) => {
      removed(type);
      listeners.delete(type);
    },
  };
});

afterEach(() => {
  delete (globalThis as { document?: unknown }).document;
});

function field() {
  const tree = screen.toJSON();
  return (Array.isArray(tree) ? tree[0] : tree) as { type: string; props: Record<string, any> };
}

describe('WedgeInput (web)', () => {
  it('is a labelled text field the scanner can type into, kept out of sight and out of the tab order', async () => {
    await render(<WedgeInput onScan={jest.fn()} />);

    const input = field();
    expect(input.type).toBe('input');
    expect(input.props['aria-label']).toBe(WEDGE_INPUT_LABEL);
    expect(input.props['data-testid']).toBe(WEDGE_INPUT_TEST_ID);
    expect(input.props.tabIndex).toBe(-1);
    expect(input.props.inputMode).toBe('none');
    expect(input.props.style.width).toBe(1);
    expect(input.props.style.display).toBeUndefined();
    expect(input.props.autoComplete).toBe('off');
  });

  it('takes focus without scrolling the page when it mounts or comes to the front', async () => {
    const view = await render(<WedgeInput onScan={jest.fn()} active={false} />);
    expect(focus).not.toHaveBeenCalled();

    await view.rerender(<WedgeInput onScan={jest.fn()} active />);

    expect(focus).toHaveBeenCalledWith({ preventScroll: true });
  });

  it('is the field Rescan focuses while its screen is in front, and none once it is not', async () => {
    const view = await render(<WedgeInput onScan={jest.fn()} />);
    focus.mockClear();

    expect(focusScannerInput()).toBe(true);
    expect(focus).toHaveBeenCalledWith({ preventScroll: true });

    await view.rerender(<WedgeInput onScan={jest.fn()} active={false} />);
    focus.mockClear();
    expect(focusScannerInput()).toBe(false);
    expect(focus).not.toHaveBeenCalled();
  });

  it('scans the trimmed text on Enter, clears the field and holds the Enter back from forms', async () => {
    const onScan = jest.fn();
    await render(<WedgeInput onScan={onScan} />);

    await act(async () => field().props.onChange({ currentTarget: { value: ' 0123456789012 ' } }));
    const enter = { key: 'Enter', preventDefault: jest.fn() };
    await act(async () => field().props.onKeyDown(enter));

    expect(onScan).toHaveBeenCalledWith('0123456789012');
    expect(enter.preventDefault).toHaveBeenCalled();
    expect(field().props.value).toBe('');
  });

  it('does not scan on other keys or an empty buffer', async () => {
    const onScan = jest.fn();
    await render(<WedgeInput onScan={onScan} />);

    await act(async () => field().props.onChange({ currentTarget: { value: '0123' } }));
    await act(async () => field().props.onKeyDown({ key: '4', preventDefault: jest.fn() }));
    expect(onScan).not.toHaveBeenCalled();

    await act(async () => field().props.onChange({ currentTarget: { value: '   ' } }));
    await act(async () => field().props.onKeyDown({ key: 'Enter', preventDefault: jest.fn() }));
    expect(onScan).not.toHaveBeenCalled();
  });

  it('listens for a scan typed anywhere on the page only while the screen is in front', async () => {
    const onScan = jest.fn();
    const view = await render(<WedgeInput onScan={onScan} active={false} />);
    expect(added).not.toHaveBeenCalled();

    await view.rerender(<WedgeInput onScan={onScan} active />);
    expect(added).toHaveBeenCalledWith('keydown', true);

    const keys = [...'0123456789012', 'Enter'];
    let at = 0;
    jest.spyOn(Date, 'now').mockImplementation(() => (at += 10));
    for (const key of keys) {
      listeners.get('keydown')?.({ key, target: { tagName: 'BODY' }, preventDefault: jest.fn(), stopPropagation: jest.fn() });
    }
    expect(onScan).toHaveBeenCalledWith('0123456789012');

    await view.rerender(<WedgeInput onScan={onScan} active={false} />);
    expect(removed).toHaveBeenCalledWith('keydown');
    jest.restoreAllMocks();
  });

  it('hands a page scan to the latest callback', async () => {
    const first = jest.fn();
    const second = jest.fn();
    const view = await render(<WedgeInput onScan={first} />);
    await view.rerender(<WedgeInput onScan={second} />);

    let at = 0;
    jest.spyOn(Date, 'now').mockImplementation(() => (at += 10));
    for (const key of [...'STOCK_IN', 'Enter']) {
      listeners.get('keydown')?.({ key, target: { tagName: 'BODY' }, preventDefault: jest.fn(), stopPropagation: jest.fn() });
    }

    expect(first).not.toHaveBeenCalled();
    expect(second).toHaveBeenCalledWith('STOCK_IN');
    jest.restoreAllMocks();
  });
});
