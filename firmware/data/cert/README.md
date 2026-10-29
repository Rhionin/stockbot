# Mozilla CA certificate bundle (`x509_crt_bundle.bin`)

Embedded for `WiFiClientSecure::setCACertBundle()` so HTTPS calls to public hosts (e.g. `stockbot.rhionin.com`) verify without a single hard-coded root CA.

## Provenance

| Field | Value |
| --- | --- |
| Source PEM | [ESP-IDF `cacrt_all.pem`](https://github.com/espressif/esp-idf/blob/v4.4.7/components/mbedtls/esp_crt_bundle/cacrt_all.pem) (tag **v4.4.7**, matches Arduino-ESP32 2.0.17 / `espressif32@6.9.0`) |
| Generator | [ESP-IDF `gen_crt_bundle.py`](https://github.com/espressif/esp-idf/blob/v4.4.7/components/mbedtls/esp_crt_bundle/gen_crt_bundle.py) |
| Output file | `x509_crt_bundle.bin` (63376 bytes, 135 certificates) |
| SHA256 | `368e89b3e6d302e4734e7fbded33a4cbcf59427149b73212ffe12d00f5fad63c` |

PlatformIO embeds this file via `board_build.embed_files = data/cert/x509_crt_bundle.bin` in `platformio.ini`.

## Regenerate

From a checkout of ESP-IDF v4.4.7 (or download the two files below):

```bash
pip install cryptography
curl -sL -o cacrt_all.pem \
  https://raw.githubusercontent.com/espressif/esp-idf/v4.4.7/components/mbedtls/esp_crt_bundle/cacrt_all.pem
curl -sL -o gen_crt_bundle.py \
  https://raw.githubusercontent.com/espressif/esp-idf/v4.4.7/components/mbedtls/esp_crt_bundle/gen_crt_bundle.py
python3 gen_crt_bundle.py -i cacrt_all.pem -q
cp x509_crt_bundle ../firmware/data/cert/x509_crt_bundle.bin
sha256sum ../firmware/data/cert/x509_crt_bundle.bin
```

Update the SHA256 line in this README after regenerating.
