export const WEDGE_INPUT_TEST_ID = 'barcode-scanner-input';
export const WEDGE_INPUT_LABEL = 'Barcode scanner input';

export interface WedgeInputProps {
  onScan: (barcode: string) => void;
  // Tab screens stay mounted when left, and only the one in front may read a scan.
  active?: boolean;
  // Takes the cursor when it turns true. A sheet closing must not, or focus would not go back to its button.
  takeFocus?: boolean;
}
