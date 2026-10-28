#include "hal_scanner.h"

#ifdef ARDUINO

#if defined(USE_USB_HOST_HID)

#include <usb/usb_host.h>

#include "hid_keycode.h"

namespace stockbot {

namespace {

std::function<void(const ScanLine&)> g_hid_scan_callback;
HidKeycodeDecoder g_hid_decoder;
bool g_usb_host_installed = false;
usb_host_client_handle_t g_usb_client = nullptr;
TaskHandle_t g_usb_task = nullptr;

void usbHostLibTask(void* /*param*/) {
  while (true) {
    uint32_t event_flags = 0;
    usb_host_lib_handle_events(portMAX_DELAY, &event_flags);
    if (event_flags & USB_HOST_LIB_EVENT_FLAGS_NO_CLIENTS) {
      usb_host_device_free_all();
    }
  }
}

void usbClientEventCallback(const usb_host_client_event_msg_t* event_msg, void* /*arg*/) {
  if (event_msg->event != USB_HOST_CLIENT_EVENT_NEW_DEV) {
    return;
  }
  usb_device_handle_t dev_hdl = nullptr;
  if (usb_host_device_open(g_usb_client, event_msg->new_dev.address, &dev_hdl) != ESP_OK) {
    return;
  }
  usb_host_interface_claim(g_usb_client, dev_hdl, 0, 0);
}

void ensureUsbHostStarted() {
  if (g_usb_host_installed) {
    return;
  }
  const usb_host_config_t host_config = {
      .skip_phy_setup = false,
      .intr_flags = ESP_INTR_FLAG_LEVEL1,
  };
  if (usb_host_install(&host_config) != ESP_OK) {
    return;
  }
  const usb_host_client_config_t client_config = {
      .is_synchronous = false,
      .max_num_event_msg = 5,
      .async =
          {
              .client_event_callback = usbClientEventCallback,
              .callback_arg = nullptr,
          },
  };
  usb_host_client_register(&client_config, &g_usb_client);
  xTaskCreate(usbHostLibTask, "usb_host", 4096, nullptr, 5, &g_usb_task);
  g_usb_host_installed = true;
}

class UsbHidScanner final : public HalScanner {
 public:
  void begin() override {
    g_hid_decoder.resetLine();
    ensureUsbHostStarted();
  }

  void enable1DOnly(bool) override {}

  void setContinuousMode(bool) override {}

  void poll(uint64_t now_ms, const std::function<void(const ScanLine&)>& on_scan) override {
    g_hid_scan_callback = on_scan;
    if (g_usb_client != nullptr) {
      usb_host_client_handle_events(g_usb_client, 0);
    }
    (void)now_ms;
  }
};

UsbHidScanner g_usb_hid;

}  // namespace

HalScanner* createUsbHidScanner() { return &g_usb_hid; }

void stockbotOnHidKeyboardReport(const uint8_t report[8], uint64_t now_ms) {
  if (!g_hid_scan_callback || report == nullptr) {
    return;
  }
  if (auto line = g_hid_decoder.consumeReport(report)) {
    g_hid_scan_callback(ScanLine{*line, now_ms});
  }
}

}  // namespace stockbot

#else

#include "hid_keycode.h"

namespace stockbot {

namespace {

std::function<void(const ScanLine&)> g_hid_scan_callback;
HidKeycodeDecoder g_hid_decoder;

class UsbHidScanner final : public HalScanner {
 public:
  void begin() override { g_hid_decoder.resetLine(); }
  void enable1DOnly(bool) override {}
  void setContinuousMode(bool) override {}
  void poll(uint64_t now_ms, const std::function<void(const ScanLine&)>& on_scan) override {
    g_hid_scan_callback = on_scan;
    (void)now_ms;
  }
};

UsbHidScanner g_usb_hid;

}  // namespace

HalScanner* createUsbHidScanner() { return &g_usb_hid; }

void stockbotOnHidKeyboardReport(const uint8_t report[8], uint64_t now_ms) {
  if (!g_hid_scan_callback || report == nullptr) {
    return;
  }
  if (auto line = g_hid_decoder.consumeReport(report)) {
    g_hid_scan_callback(ScanLine{*line, now_ms});
  }
}

}  // namespace stockbot

#endif
#endif
