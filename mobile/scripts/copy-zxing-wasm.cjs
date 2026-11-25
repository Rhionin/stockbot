const crypto = require('crypto');
const fs = require('fs');
const path = require('path');

const PUBLIC_WASM_PATH = path.join('zxing', 'zxing_reader.wasm');

function installedWasm() {
  const detectorEntry = require.resolve('barcode-detector', { paths: [__dirname] });
  return require.resolve('zxing-wasm/reader/zxing_reader.wasm', { paths: [path.dirname(detectorEntry)] });
}

// The detector pins the exact build it expects. A different file would decode
// wrongly or not at all, so the copy fails the build instead.
function copyZxingWasm({ mobileDir = path.resolve(__dirname, '..'), fsImpl = fs } = {}) {
  const { ZXING_WASM_SHA256: expected } = require('barcode-detector');
  const bytes = fsImpl.readFileSync(installedWasm());
  const actual = crypto.createHash('sha256').update(bytes).digest('hex');
  if (actual !== expected) {
    throw new Error(`zxing_reader.wasm has sha256 ${actual}, but barcode-detector expects ${expected}`);
  }
  const target = path.join(mobileDir, 'public', PUBLIC_WASM_PATH);
  fsImpl.mkdirSync(path.dirname(target), { recursive: true });
  fsImpl.writeFileSync(target, bytes);
  return { target, bytes: bytes.length, sha256: actual };
}

if (require.main === module) {
  try {
    const { target, bytes } = copyZxingWasm();
    console.log(`zxing: ${bytes} bytes -> ${path.relative(process.cwd(), target)}`);
  } catch (error) {
    console.error(error instanceof Error ? error.message : error);
    process.exit(1);
  }
}

module.exports = { copyZxingWasm, PUBLIC_WASM_PATH };
