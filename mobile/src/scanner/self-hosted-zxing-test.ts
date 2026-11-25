import { ZXING_WASM_URL, installSelfHostedZxing, zxingLocateFile } from './selfHostedZxing.web';

describe('self-hosted ZXing', () => {
  it('points the WASM at this origin and leaves other files where the loader put them', () => {
    expect(zxingLocateFile('zxing_reader.wasm', 'https://fastly.jsdelivr.net/')).toBe(ZXING_WASM_URL);
    expect(ZXING_WASM_URL.startsWith('/')).toBe(true);
    expect(zxingLocateFile('glue.js', '/base/')).toBe('/base/glue.js');
  });

  it('overrides the loader once, however many screens ask', async () => {
    const setZXingModuleOverrides = jest.fn();
    const load = jest.fn(async () => ({ setZXingModuleOverrides }));

    await installSelfHostedZxing(load);
    await installSelfHostedZxing(load);

    expect(load).toHaveBeenCalledTimes(1);
    expect(setZXingModuleOverrides).toHaveBeenCalledTimes(1);
    const overrides = setZXingModuleOverrides.mock.calls[0][0];
    expect(overrides.locateFile('zxing_reader.wasm', 'https://elsewhere/')).toBe(ZXING_WASM_URL);
  });
});
