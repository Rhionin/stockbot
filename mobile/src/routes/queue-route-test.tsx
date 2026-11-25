import { router } from 'expo-router';
import { render } from '@testing-library/react-native';
import { isWeb } from '@/ui/layout';
import { QueueRoute } from './QueueRoute';

const mockQueue: { onRescan: () => void } = { onRescan: () => undefined };

jest.mock('expo-router', () => ({
  router: { navigate: jest.fn() },
  useFocusEffect: jest.fn(),
}));

jest.mock('@/session', () => ({
  useSession: () => ({ status: 'ready', scope: 'scope', client: {}, warning: '' }),
}));

jest.mock('@/scanner/focusScannerInput', () => ({ focusScannerInput: () => false }));

jest.mock('@/ui/layout', () => ({ isWeb: jest.fn(() => false) }));

jest.mock('@/screens/QueueScreen', () => ({
  QueueScreen: (props: { onRescan: () => void }) => {
    mockQueue.onRescan = props.onRescan;
    return null;
  },
}));

describe('QueueRoute Rescan', () => {
  beforeEach(() => jest.clearAllMocks());

  it('goes to the Scan tab on the phone, where the scan route is hidden', async () => {
    jest.mocked(isWeb).mockReturnValue(false);
    await render(<QueueRoute />);

    mockQueue.onRescan();

    expect(router.navigate).toHaveBeenCalledTimes(1);
    expect(router.navigate).toHaveBeenCalledWith('/');
  });

  it('keeps the scan route on the web', async () => {
    jest.mocked(isWeb).mockReturnValue(true);
    await render(<QueueRoute />);

    mockQueue.onRescan();

    expect(router.navigate).toHaveBeenCalledWith('/scan');
  });
});
