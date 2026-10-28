#include "hal_scanner.h"

#ifdef ARDUINO

#include <Wire.h>

#include <M5Unified.h>

#include <cmath>

#include "board_pins.h"

namespace stockbot {

namespace {

#ifndef U173_I2C_ADDR
#define U173_I2C_ADDR 0x21
#endif

class U173I2CScanner final : public HalScanner {
 public:
  void begin() override {
    Wire.begin(PIN_GROVE_SDA, PIN_GROVE_SCL);
    Wire.setClock(400000);
  }

  void enable1DOnly(bool enabled) override { writeReg16(0x0030, enabled ? 0x0001 : 0x0000); }

  void setContinuousMode(bool enabled) override { continuous_ = enabled; }

  void poll(uint64_t now_ms, const std::function<void(const ScanLine&)>& on_scan) override {
#if TARGET_STICKS3
    if (continuous_ && M5.Power.getVBUSVoltage() > 4000) {
      float ax = 0;
      float ay = 0;
      float az = 0;
      M5.Imu.getAccel(&ax, &ay, &az);
      const float delta = std::fabs(std::sqrt(ax * ax + ay * ay + az * az) - 1.0f);
      if (delta >= 0.08f) {
        return;
      }
      writeReg16(0x0000, 0x0001);
    }
#endif
    if (readReg16(0x0010) != 1) {
      return;
    }
    const uint16_t len = readReg16(0x0020);
    if (len == 0 || len > 64) {
      writeReg16(0x0010, 0);
      return;
    }
    std::string barcode;
    barcode.reserve(len);
    Wire.beginTransmission(U173_I2C_ADDR);
    Wire.write(0x10);
    Wire.write(0x00);
    Wire.endTransmission(false);
    Wire.requestFrom(U173_I2C_ADDR, static_cast<uint8_t>(len));
    while (Wire.available()) {
      barcode.push_back(static_cast<char>(Wire.read()));
    }
    writeReg16(0x0010, 0);
    if (!barcode.empty()) {
      on_scan(ScanLine{barcode, now_ms});
    }
  }

 private:
  bool continuous_ = false;

  void writeReg16(uint16_t reg, uint16_t value) {
    Wire.beginTransmission(U173_I2C_ADDR);
    Wire.write(static_cast<uint8_t>(reg >> 8));
    Wire.write(static_cast<uint8_t>(reg & 0xFF));
    Wire.write(static_cast<uint8_t>(value >> 8));
    Wire.write(static_cast<uint8_t>(value & 0xFF));
    Wire.endTransmission();
  }

  uint16_t readReg16(uint16_t reg) {
    Wire.beginTransmission(U173_I2C_ADDR);
    Wire.write(static_cast<uint8_t>(reg >> 8));
    Wire.write(static_cast<uint8_t>(reg & 0xFF));
    Wire.endTransmission(false);
    Wire.requestFrom(U173_I2C_ADDR, static_cast<uint8_t>(2));
    if (Wire.available() < 2) {
      return 0;
    }
    const uint8_t hi = Wire.read();
    const uint8_t lo = Wire.read();
    return static_cast<uint16_t>((hi << 8) | lo);
  }
};

U173I2CScanner g_u173_i2c;

}  // namespace

HalScanner* createU173I2cScanner() { return &g_u173_i2c; }

}  // namespace stockbot

#endif
