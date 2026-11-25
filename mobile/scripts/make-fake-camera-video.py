"""Writes the MJPEG that Chromium plays as a fake camera in the web e2e.

    python3 scripts/make-fake-camera-video.py e2e/fixtures/ean13-4006381333931.mjpeg

One synthetic EAN-13 on a white card, centered in a 640x480 frame. Chromium
loops a single-frame MJPEG for --use-file-for-fake-video-capture.
"""
import io
import sys

from PIL import Image, ImageDraw

DIGITS = "4006381333931"
L = ["0001101", "0011001", "0010011", "0111101", "0100011", "0110001", "0101111", "0111011", "0110111", "0001011"]
G = ["0100111", "0110011", "0011011", "0100001", "0011101", "0111001", "0000101", "0010001", "0001001", "0010111"]
R = ["1110010", "1100110", "1101100", "1000010", "1011100", "1001110", "1010000", "1000100", "1001000", "1110100"]
PARITY = ["LLLLLL", "LLGLGG", "LLGGLG", "LLGGGL", "LGLLGG", "LGGLLG", "LGGGLL", "LGLGLG", "LGLGGL", "LGGLGL"]


def modules(code: str) -> str:
    digits = [int(c) for c in code]
    check = (10 - sum(d * (3 if i % 2 else 1) for i, d in enumerate(digits[:12])) % 10) % 10
    if check != digits[12]:
        raise SystemExit(f"{code} has check digit {digits[12]}, expected {check}")
    pattern = PARITY[digits[0]]
    left = "".join((L if p == "L" else G)[d] for p, d in zip(pattern, digits[1:7]))
    right = "".join(R[d] for d in digits[7:])
    return "101" + left + "01010" + right + "101"


def frame(code: str, width: int = 640, height: int = 480, module_px: int = 4) -> bytes:
    bars = modules(code)
    image = Image.new("RGB", (width, height), "white")
    draw = ImageDraw.Draw(image)
    x0 = (width - len(bars) * module_px) // 2
    top, bottom = height // 2 - 90, height // 2 + 90
    for i, bit in enumerate(bars):
        if bit == "1":
            draw.rectangle([x0 + i * module_px, top, x0 + (i + 1) * module_px - 1, bottom], fill="black")
    out = io.BytesIO()
    image.save(out, "JPEG", quality=92)
    return out.getvalue()


if __name__ == "__main__":
    with open(sys.argv[1], "wb") as handle:
        handle.write(frame(DIGITS))
