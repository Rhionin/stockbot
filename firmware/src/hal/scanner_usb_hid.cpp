#include "hal_scanner.h"

#ifdef ARDUINO

#if defined(USE_USB_HOST_HID)

#include <cstring>

#include <freertos/FreeRTOS.h>
#include <freertos/task.h>

#include <usb/usb_host.h>
#include <usb/usb_types_ch9.h>

#include "hid_keycode.h"

namespace stockbot {

void stockbotOnHidKeyboardReport(const uint8_t report[8], uint64_t now_ms);

namespace {

std::function<void(const ScanLine&)> g_hid_scan_callback;
HidKeycodeDecoder g_hid_decoder;
bool g_usb_host_installed = false;
usb_host_client_handle_t g_usb_client = nullptr;
TaskHandle_t g_usb_task = nullptr;

usb_device_handle_t g_open_dev = nullptr;
usb_transfer_t* g_intr_transfer = nullptr;
uint8_t g_claimed_interface = 0;

void hidInterruptInCallback(usb_transfer_t* transfer);

bool findInterruptInEndpoint(usb_device_handle_t dev_hdl, uint8_t interface_num, uint8_t* ep_addr,
                             uint16_t* max_packet) {
  const usb_config_desc_t* cfg = nullptr;
  if (usb_host_get_active_config_descriptor(dev_hdl, &cfg) != ESP_OK || cfg == nullptr) {
    return false;
  }
  const uint8_t* ptr = reinterpret_cast<const uint8_t*>(cfg);
  const uint8_t* end = ptr + cfg->wTotalLength;
  ptr += cfg->bLength;
  int current_interface = -1;
  while (ptr + 1 < end) {
    const usb_standard_desc_t* desc = reinterpret_cast<const usb_standard_desc_t*>(ptr);
    if (desc->bLength == 0 || ptr + desc->bLength > end) {
      break;
    }
    if (desc->bDescriptorType == USB_B_DESCRIPTOR_TYPE_INTERFACE) {
      const auto* intf = reinterpret_cast<const usb_intf_desc_t*>(ptr);
      current_interface = intf->bInterfaceNumber;
    } else if (desc->bDescriptorType == USB_B_DESCRIPTOR_TYPE_ENDPOINT &&
               current_interface == static_cast<int>(interface_num)) {
      const auto* ep = reinterpret_cast<const usb_ep_desc_t*>(ptr);
      const bool is_in = (ep->bEndpointAddress & USB_B_ENDPOINT_ADDRESS_EP_DIR_MASK) != 0;
      const bool is_interrupt =
          (ep->bmAttributes & USB_BM_ATTRIBUTES_XFERTYPE_MASK) == USB_BM_ATTRIBUTES_XFER_INT;
      if (is_in && is_interrupt) {
        *ep_addr = ep->bEndpointAddress;
        *max_packet = ep->wMaxPacketSize;
        if (*max_packet < 8) {
          *max_packet = 8;
        }
        if (*max_packet > 64) {
          *max_packet = 64;
        }
        return true;
      }
    }
    ptr += desc->bLength;
  }
  return false;
}

void releaseHidDevice() {
  if (g_intr_transfer != nullptr) {
    usb_host_transfer_free(g_intr_transfer);
    g_intr_transfer = nullptr;
  }
  if (g_open_dev != nullptr && g_usb_client != nullptr) {
    usb_host_interface_release(g_usb_client, g_open_dev, g_claimed_interface);
    usb_host_device_close(g_usb_client, g_open_dev);
    g_open_dev = nullptr;
  }
}

bool submitInterruptInTransfer(usb_device_handle_t dev_hdl) {
  uint8_t ep_addr = 0x81;
  uint16_t packet_size = 8;
  if (!findInterruptInEndpoint(dev_hdl, g_claimed_interface, &ep_addr, &packet_size)) {
    ep_addr = 0x81;
    packet_size = 8;
  }

  if (g_intr_transfer != nullptr) {
    usb_host_transfer_free(g_intr_transfer);
    g_intr_transfer = nullptr;
  }
  if (usb_host_transfer_alloc(packet_size, 0, &g_intr_transfer) != ESP_OK || g_intr_transfer == nullptr) {
    return false;
  }

  g_intr_transfer->device_handle = dev_hdl;
  g_intr_transfer->bEndpointAddress = ep_addr;
  g_intr_transfer->callback = hidInterruptInCallback;
  g_intr_transfer->context = nullptr;
  g_intr_transfer->num_bytes = packet_size;
  return usb_host_transfer_submit(g_intr_transfer) == ESP_OK;
}

void hidInterruptInCallback(usb_transfer_t* transfer) {
  if (transfer == nullptr) {
    return;
  }
  if (transfer->status == USB_TRANSFER_STATUS_COMPLETED && transfer->actual_num_bytes > 0) {
    uint8_t report[8] = {0};
    const size_t copy =
        transfer->actual_num_bytes < sizeof(report) ? transfer->actual_num_bytes : sizeof(report);
    std::memcpy(report, transfer->data_buffer, copy);
    stockbotOnHidKeyboardReport(report, millis());
  }
  if (transfer->status != USB_TRANSFER_STATUS_CANCELED && g_open_dev != nullptr) {
    usb_host_transfer_submit(transfer);
  }
}

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
  if (event_msg == nullptr) {
    return;
  }
  if (event_msg->event == USB_HOST_CLIENT_EVENT_DEV_GONE) {
    if (g_open_dev == event_msg->dev_gone.dev_hdl) {
      releaseHidDevice();
    }
    return;
  }
  if (event_msg->event != USB_HOST_CLIENT_EVENT_NEW_DEV) {
    return;
  }

  releaseHidDevice();

  usb_device_handle_t dev_hdl = nullptr;
  if (usb_host_device_open(g_usb_client, event_msg->new_dev.address, &dev_hdl) != ESP_OK) {
    return;
  }
  g_claimed_interface = 0;
  if (usb_host_interface_claim(g_usb_client, dev_hdl, g_claimed_interface, 0) != ESP_OK) {
    usb_host_device_close(g_usb_client, dev_hdl);
    return;
  }
  g_open_dev = dev_hdl;
  if (!submitInterruptInTransfer(dev_hdl)) {
    releaseHidDevice();
  }
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
