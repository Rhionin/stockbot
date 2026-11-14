//! Host-portable scanner core logic (barcode filter, text normalization).
#![forbid(unsafe_code)]

mod barcode;
mod button_hold_tracker;
mod device_hal;
mod display_model;
mod hid_keycode;
mod nvs_config;
mod ota_controller;
mod ota_manifest;
mod ota_semver;
mod protocol_json;
mod ring_buffer;
mod scanner_controller;
mod scanner_ports;
mod scanner_state;
mod setup_network;
mod text;

pub use device_hal::{ota_nvs_keys, OtaSlot, OtaSlotsHal};
pub use ota_controller::{
    DeviceOtaError, OtaController, OtaHeartbeatFields, OtaTickContext, UpdateOffer,
    CHECKIN_DEADLINE_MS, INSTALL_WINDOW_IDLE_MS, MAX_RANGE_RETRIES, MIN_BATTERY_PCT_STICKS3,
    MIN_WIFI_RSSI_DBM,
};
pub use ota_manifest::{
    canonical_bytes, check_size_fits_partition, check_target, decode_public_key_b64,
    parse_canonical, validate_fields, verify_canonical, verify_fields, verify_pre_download, Fields,
    ManifestError, OtaErrorCode, TrustedKeyTable, MAX_MANIFEST_SIZE, PARTITION_SIZE_BYTES,
};
pub use ota_semver::{validate_semver2, versions_differ};

pub use barcode::{
    accept_with_duplicate_cooldown, classify_barcode, is_pairing_code, looks_like_pairing_code,
    pairing_check_character, BarcodeKind, Direction, DuplicateCooldownState, OperationalState,
    RESCAN_COOLDOWN_MS,
};
pub use button_hold_tracker::{ButtonHoldTracker, ModeButtonHoldPhase, ModeButtonReleaseAction};
pub use display_model::{
    apply_display_band_colors, build_display_model, display_utf8_char_count,
    format_band2_barcode_keep_end, format_revert_countdown, render_display_bands, rgb565_from_hex,
    wrap_product_name_two_lines, wrap_product_name_two_lines_default, AudioCue, DisplayBands,
    DisplayModelInput, ScanResultView, ScanStatusKind, UiScreen, BAND2_CHARS_PER_LINE,
    BAND3_CHARS_PER_LINE, ELLIPSIS, SCANNER_SETUP_AP_PREFIX,
};
pub use hid_keycode::{HidKeycodeDecoder, HID_KEY_BACKSPACE, HID_KEY_ENTER};
pub use nvs_config::NvsConfig;
pub use protocol_json::{
    build_batch_scans_request_json, build_heartbeat_request_json, build_pair_complete_request_json,
    build_single_scan_request_json, http_failure_is_retryable,
    pairing_complete_failure_is_client_rejected, pairing_complete_failure_is_expired,
    pairing_complete_failure_is_rate_limited, parse_api_error_code,
    parse_device_scan_batch_response_json, parse_device_scan_response_json,
    parse_heartbeat_response_json, parse_pair_complete_response_json, parse_setup_qr_payload,
    BatchScanResultItem, DeviceHubConfigParsed, HeartbeatParsed, PairCompleteParsed,
    ParsedDeviceScanResponse, SetupQrParsed,
};
pub use ring_buffer::{
    c_str_from_fixed, crc32_ring_record, crc32_update, make_record, ring_scan_age_ms, sbr1,
    RingBuffer, RingEnqueueError, RingIoError, RingScanRecord, RingStorage, RING_CAPACITY,
};
pub use scanner_controller::ScannerController;
pub use scanner_ports::{
    CaptivePortalHal, CaptivePortalSubmission, ClockHal, DeviceClient, DisplayHal, FeedbackHal,
    FirmwareHttpHal, FirmwareOpenResult, HttpCallResult, NvsStore, PowerHal, PowerStatus, ScanLine,
    ScannerHal, WifiStationHal,
};
pub use scanner_state::{
    BacklightLevel, ButtonHoldGesture, CommitMode, ModeButtonResult, ModeChime, ScannerState,
    DOCK_SUPPRESS_MS,
};
pub use setup_network::{ap_suffix_from_mac_address, scanner_setup_ssid};
pub use text::{is_ascii_digit_product_barcode, normalize_scan, trim_ascii};
