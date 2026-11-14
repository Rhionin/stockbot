//! OTA install state machine (firmware-ota.md §6–§7).

use sha2::{Digest, Sha256};

use crate::device_hal::{ota_nvs_keys, OtaSlotsHal};
use crate::display_model::DisplayBands;
use crate::ota_manifest::{verify_pre_download, Fields, ManifestError, TrustedKeyTable};
use crate::ota_semver::versions_differ;
use crate::scanner_ports::{ClockHal, DisplayHal, FirmwareHttpHal, NvsStore, PowerHal};

pub const INSTALL_WINDOW_IDLE_MS: u64 = 120_000;
pub const CHECKIN_DEADLINE_MS: u64 = 600_000;
pub const MAX_RANGE_RETRIES: u32 = 3;
pub const MIN_WIFI_RSSI_DBM: i32 = -80;
pub const MIN_BATTERY_PCT_STICKS3: i32 = 50;
const DOWNLOAD_CHUNK_BYTES: usize = 1024;

/// Server `update` object from heartbeat (`FirmwareUpdate` in api-contract).
#[derive(Clone, Debug, PartialEq, Eq)]
pub struct UpdateOffer {
    pub release_id: String,
    pub fields: Fields,
    pub sig_b64: String,
    pub url: String,
}

/// Reported heartbeat `otaState` and related fields.
#[derive(Clone, Debug, PartialEq, Eq)]
pub struct OtaHeartbeatFields {
    pub ota_state: &'static str,
    pub ota_release_id: Option<String>,
    pub ota_error: Option<&'static str>,
    pub ota_progress_pct: Option<u8>,
    pub rollback: bool,
}

impl Default for OtaHeartbeatFields {
    fn default() -> Self {
        Self {
            ota_state: "idle",
            ota_release_id: None,
            ota_error: None,
            ota_progress_pct: None,
            rollback: false,
        }
    }
}

/// Device-reported OTA error strings (api-contract).
#[derive(Clone, Copy, Debug, PartialEq, Eq)]
pub enum DeviceOtaError {
    WrongTarget,
    UnknownKey,
    BadSignature,
    TooLarge,
    DownloadFailed,
    HttpStatus,
    BadSize,
    BadSha256,
    FlashWriteFailed,
    ImageInvalid,
    CheckinTimeout,
    BootFailed,
}

impl DeviceOtaError {
    pub const fn as_str(self) -> &'static str {
        match self {
            Self::WrongTarget => "wrong_target",
            Self::UnknownKey => "unknown_key",
            Self::BadSignature => "bad_signature",
            Self::TooLarge => "too_large",
            Self::DownloadFailed => "download_failed",
            Self::HttpStatus => "http_status",
            Self::BadSize => "bad_size",
            Self::BadSha256 => "bad_sha256",
            Self::FlashWriteFailed => "flash_write_failed",
            Self::ImageInvalid => "image_invalid",
            Self::CheckinTimeout => "checkin_timeout",
            Self::BootFailed => "boot_failed",
        }
    }

    fn from_manifest(err: &ManifestError) -> Self {
        match err {
            ManifestError::WrongTarget => Self::WrongTarget,
            ManifestError::UnknownKey => Self::UnknownKey,
            ManifestError::TooLarge => Self::TooLarge,
            ManifestError::BadSignature | ManifestError::Validation(_) => Self::BadSignature,
        }
    }
}

#[derive(Clone, Copy, Debug, PartialEq, Eq)]
enum Phase {
    Idle,
    Offered,
    Waiting,
    Downloading,
    Verifying,
    Rebooting,
    PendingVerify,
}

/// Per-tick environment for install-window and user-preemption (§6).
#[derive(Clone, Debug, Default, PartialEq, Eq)]
pub struct OtaTickContext {
    pub paired: bool,
    pub ring_empty: bool,
    pub last_activity_ms: u64,
    pub user_scan_this_tick: bool,
    pub user_button_this_tick: bool,
    pub in_pairing: bool,
    pub in_captive_portal: bool,
    pub factory_reset_hold: bool,
    pub wifi_rssi_dbm: Option<i32>,
    pub running_version: String,
}

pub struct OtaController<'a> {
    clock: &'a dyn ClockHal,
    display: &'a mut dyn DisplayHal,
    power: &'a dyn PowerHal,
    nvs: &'a mut dyn NvsStore,
    http: &'a mut dyn FirmwareHttpHal,
    slots: &'a mut dyn OtaSlotsHal,
    trusted_keys: &'a TrustedKeyTable,
    device_target: String,
    battery_powered: bool,

    phase: Phase,
    offer: Option<UpdateOffer>,
    verified_fields: Option<Fields>,
    download_retries: u32,
    hasher: Option<Sha256>,
    boot_ms: u64,
    one_shot: Option<OtaHeartbeatFields>,
    suppress_release_id: String,
    pending_rollback_report: bool,
    rollback_reported: bool,
    http_body_open: bool,
}

impl<'a> OtaController<'a> {
    #[allow(clippy::too_many_arguments)]
    pub fn new(
        clock: &'a dyn ClockHal,
        display: &'a mut dyn DisplayHal,
        power: &'a dyn PowerHal,
        nvs: &'a mut dyn NvsStore,
        http: &'a mut dyn FirmwareHttpHal,
        slots: &'a mut dyn OtaSlotsHal,
        trusted_keys: &'a TrustedKeyTable,
        device_target: impl Into<String>,
        battery_powered: bool,
    ) -> Self {
        let device_target = device_target.into();
        let suppress_release_id = nvs
            .stockbot_get_str(ota_nvs_keys::OTA_SUPP)
            .unwrap_or_default();
        let pending_rollback_report = slots.last_invalid_partition().is_some();
        Self {
            clock,
            display,
            power,
            nvs,
            http,
            slots,
            trusted_keys,
            device_target,
            battery_powered,
            phase: Phase::Idle,
            offer: None,
            verified_fields: None,
            download_retries: 0,
            hasher: None,
            boot_ms: clock.now_ms(),
            one_shot: None,
            suppress_release_id,
            pending_rollback_report,
            rollback_reported: false,
            http_body_open: false,
        }
    }

    pub fn sleep_inhibited(&self) -> bool {
        matches!(self.phase, Phase::PendingVerify)
    }

    pub fn phase_is_downloading(&self) -> bool {
        matches!(self.phase, Phase::Downloading)
    }

    /// Call once after boot when the running image is `PENDING_VERIFY`.
    pub fn enter_pending_verify_after_boot(&mut self, release_id: impl Into<String>) {
        self.phase = Phase::PendingVerify;
        self.boot_ms = self.clock.now_ms();
        let rel = release_id.into();
        self.verified_fields = None;
        self.offer = Some(UpdateOffer {
            release_id: rel.clone(),
            fields: Fields {
                product: String::new(),
                target: self.device_target.clone(),
                version: String::new(),
                size: 0,
                sha256: String::new(),
                key_id: String::new(),
            },
            sig_b64: String::new(),
            url: String::new(),
        });
    }

    /// Apply heartbeat `update` (None withdraws the offer).
    pub fn on_heartbeat_update(&mut self, update: Option<UpdateOffer>, ctx: &OtaTickContext) {
        match self.phase {
            Phase::Idle => {
                if let Some(offer) = update {
                    let _ = self.try_adopt_offer(offer, ctx);
                }
            }
            Phase::Waiting | Phase::Offered => {
                if update.is_none() {
                    self.clear_offer_to_idle();
                } else if let Some(new_offer) = update {
                    let _ = self.try_adopt_offer(new_offer, ctx);
                }
            }
            _ => {}
        }
    }

    pub fn tick(&mut self, ctx: &OtaTickContext) {
        if let Some(report) = self.consume_checkin_timeout() {
            if self.one_shot.is_none() && !self.rollback_reported {
                self.one_shot = Some(report);
            }
            return;
        }
        if self.pending_rollback_report {
            self.emit_rollback_once();
            return;
        }
        match self.phase {
            Phase::Offered => self.tick_offered(ctx),
            Phase::Waiting => self.tick_waiting(ctx),
            Phase::Downloading => self.tick_downloading(ctx),
            Phase::Verifying => self.tick_verifying(),
            Phase::Rebooting => {}
            Phase::PendingVerify => {}
            Phase::Idle => {}
        }
    }

    /// Heartbeat HTTP outcome while `pending_verify` or suppression clearing.
    pub fn on_heartbeat_http(
        &mut self,
        transport_ok: bool,
        http_status: i32,
        heartbeat_update: Option<&UpdateOffer>,
    ) {
        if self.phase == Phase::PendingVerify {
            if transport_ok && http_status == 200 && self.slots.mark_running_valid().is_ok() {
                let rel = self
                    .offer
                    .as_ref()
                    .map(|o| o.release_id.clone())
                    .unwrap_or_default();
                self.nvs.stockbot_remove(ota_nvs_keys::OTA_ERR);
                self.one_shot = Some(OtaHeartbeatFields {
                    ota_state: "succeeded",
                    ota_release_id: Some(rel),
                    ota_error: None,
                    ota_progress_pct: None,
                    rollback: false,
                });
                self.phase = Phase::Idle;
                self.offer = None;
                self.verified_fields = None;
            }
            return;
        }
        if transport_ok && http_status == 200 {
            if let Some(update) = heartbeat_update {
                if update.release_id != self.suppress_release_id {
                    self.suppress_release_id.clear();
                    self.nvs.stockbot_remove(ota_nvs_keys::OTA_SUPP);
                }
            } else {
                self.suppress_release_id.clear();
                self.nvs.stockbot_remove(ota_nvs_keys::OTA_SUPP);
            }
        }
    }

    pub fn heartbeat_fields(&mut self) -> OtaHeartbeatFields {
        if let Some(one) = self.one_shot.take() {
            if one.ota_state == "succeeded"
                || one.ota_state == "failed"
                || one.ota_state == "rolled_back"
            {
                self.phase = Phase::Idle;
            }
            return one;
        }
        let mut out = OtaHeartbeatFields::default();
        match self.phase {
            Phase::Idle => {}
            Phase::Waiting => {
                out.ota_state = "waiting";
                out.ota_release_id = self.offer.as_ref().map(|o| o.release_id.clone());
            }
            Phase::Downloading => {
                out.ota_state = "downloading";
                out.ota_release_id = self.offer.as_ref().map(|o| o.release_id.clone());
                out.ota_progress_pct = self.progress_pct();
            }
            Phase::Verifying => {
                out.ota_state = "verifying";
                out.ota_release_id = self.offer.as_ref().map(|o| o.release_id.clone());
            }
            Phase::PendingVerify => {
                out.ota_state = "pending_verify";
                out.ota_release_id = self.offer.as_ref().map(|o| o.release_id.clone());
            }
            Phase::Offered | Phase::Rebooting => {}
        }
        out
    }

    fn try_adopt_offer(&mut self, offer: UpdateOffer, ctx: &OtaTickContext) -> bool {
        if self.should_suppress(&offer.release_id) {
            return false;
        }
        if !versions_differ(&ctx.running_version, &offer.fields.version) {
            return false;
        }
        self.offer = Some(offer);
        self.phase = Phase::Offered;
        true
    }

    fn progress_pct(&self) -> Option<u8> {
        let fields = self.verified_fields.as_ref()?;
        if fields.size <= 0 {
            return None;
        }
        let written = self.slots.bytes_written_inactive();
        let pct = (written * 100 / fields.size as u64).min(100);
        Some(pct as u8)
    }

    fn tick_offered(&mut self, ctx: &OtaTickContext) {
        let Some(offer) = self.offer.clone() else {
            self.phase = Phase::Idle;
            return;
        };
        let canonical = match crate::ota_manifest::canonical_bytes(&offer.fields) {
            Ok(b) => b,
            Err(e) => {
                self.fail_pre_download(&offer, DeviceOtaError::from_manifest(&e));
                return;
            }
        };
        match verify_pre_download(
            &canonical,
            &offer.sig_b64,
            &offer.fields.key_id,
            &self.device_target,
            self.trusted_keys,
        ) {
            Ok(fields) => {
                self.verified_fields = Some(fields);
                self.phase = Phase::Waiting;
                let _ = ctx;
            }
            Err(e) => self.fail_pre_download(&offer, DeviceOtaError::from_manifest(&e)),
        }
    }

    fn fail_pre_download(&mut self, offer: &UpdateOffer, err: DeviceOtaError) {
        if self.rollback_reported || self.pending_rollback_report {
            return;
        }
        debug_assert_eq!(self.slots.bytes_written_inactive(), 0);
        self.one_shot = Some(OtaHeartbeatFields {
            ota_state: "failed",
            ota_release_id: Some(offer.release_id.clone()),
            ota_error: Some(err.as_str()),
            ota_progress_pct: None,
            rollback: false,
        });
        self.clear_offer_to_idle();
    }

    fn fail_post_offer(&mut self, err: DeviceOtaError) {
        if self.rollback_reported || self.pending_rollback_report {
            return;
        }
        self.http_close();
        let rel = self
            .offer
            .as_ref()
            .map(|o| o.release_id.clone())
            .unwrap_or_default();
        self.nvs
            .stockbot_set_str(ota_nvs_keys::OTA_ERR, err.as_str());
        self.slots.abort_inactive_write();
        self.hasher = None;
        self.one_shot = Some(OtaHeartbeatFields {
            ota_state: "failed",
            ota_release_id: Some(rel),
            ota_error: Some(err.as_str()),
            ota_progress_pct: None,
            rollback: false,
        });
        self.clear_offer_to_idle();
    }

    fn tick_waiting(&mut self, ctx: &OtaTickContext) {
        if self.install_window_open(ctx) {
            self.begin_download();
        }
    }

    fn tick_downloading(&mut self, ctx: &OtaTickContext) {
        if ctx.user_scan_this_tick || ctx.user_button_this_tick {
            self.abort_download_to_waiting();
            return;
        }
        self.render_updating();
        let Some(fields) = self.verified_fields.clone() else {
            self.abort_download_to_waiting();
            return;
        };
        let target_size = fields.size as u64;
        loop {
            let written = self.slots.bytes_written_inactive();
            if written >= target_size {
                self.http_close();
                self.phase = Phase::Verifying;
                self.display.clear_ota_overlay();
                return;
            }
            if !self.http_body_open && !self.open_download_stream(written) {
                return;
            }
            let mut buf = [0u8; DOWNLOAD_CHUNK_BYTES];
            match self.http.read_firmware_chunk(&mut buf) {
                Ok(0) => {
                    self.http_close();
                    if self.slots.bytes_written_inactive() < target_size {
                        self.download_retries += 1;
                        if self.download_retries >= MAX_RANGE_RETRIES {
                            self.abort_download_to_waiting();
                        }
                    }
                    break;
                }
                Ok(n) => {
                    let chunk = &buf[..n];
                    if let Some(h) = self.hasher.as_mut() {
                        h.update(chunk);
                    }
                    if self.slots.write_inactive(chunk).is_err() {
                        self.fail_post_offer(DeviceOtaError::FlashWriteFailed);
                        return;
                    }
                }
                Err(_) => {
                    self.http_close();
                    self.download_retries += 1;
                    if self.download_retries >= MAX_RANGE_RETRIES {
                        self.abort_download_to_waiting();
                    }
                    break;
                }
            }
        }
    }

    fn open_download_stream(&mut self, range_start: u64) -> bool {
        let url = self
            .offer
            .as_ref()
            .map(|o| o.url.clone())
            .unwrap_or_default();
        let open = self.http.open_firmware(&url, range_start);
        if !open.transport_ok {
            self.download_retries += 1;
            if self.download_retries >= MAX_RANGE_RETRIES {
                self.abort_download_to_waiting();
            }
            return false;
        }
        if range_start > 0 {
            if open.http_status != 206 {
                self.fail_post_offer(DeviceOtaError::HttpStatus);
                return false;
            }
        } else if open.http_status != 200 {
            self.fail_post_offer(DeviceOtaError::HttpStatus);
            return false;
        }
        self.http_body_open = true;
        true
    }

    fn http_close(&mut self) {
        if self.http_body_open {
            self.http.close_firmware();
            self.http_body_open = false;
        }
    }

    fn tick_verifying(&mut self) {
        let Some(fields) = self.verified_fields.clone() else {
            self.clear_offer_to_idle();
            return;
        };
        let written = self.slots.bytes_written_inactive();
        if written != fields.size as u64 {
            self.fail_post_offer(DeviceOtaError::BadSize);
            return;
        }
        let digest = match self.hasher.take() {
            Some(h) => h.finalize(),
            None => Sha256::new().finalize(),
        };
        let hex = hex_lower(&digest);
        if hex != fields.sha256 {
            self.fail_post_offer(DeviceOtaError::BadSha256);
            return;
        }
        if self.slots.end_inactive_write().is_err() {
            self.fail_post_offer(DeviceOtaError::ImageInvalid);
            return;
        }
        if let Some(offer) = self.offer.as_ref() {
            self.nvs
                .stockbot_set_str(ota_nvs_keys::OTA_REL, &offer.release_id);
        }
        if self.slots.set_boot_inactive().is_err() {
            self.nvs.stockbot_remove(ota_nvs_keys::OTA_REL);
            self.fail_post_offer(DeviceOtaError::ImageInvalid);
            return;
        }
        self.phase = Phase::Rebooting;
        self.slots.reboot();
    }

    fn begin_download(&mut self) {
        if self.slots.begin_inactive_write().is_err() {
            self.fail_post_offer(DeviceOtaError::FlashWriteFailed);
            return;
        }
        self.hasher = Some(Sha256::new());
        self.download_retries = 0;
        self.http_close();
        self.phase = Phase::Downloading;
    }

    fn abort_download_to_waiting(&mut self) {
        self.http_close();
        self.slots.abort_inactive_write();
        self.hasher = None;
        self.phase = Phase::Waiting;
        self.display.clear_ota_overlay();
    }

    fn clear_offer_to_idle(&mut self) {
        self.http_close();
        self.offer = None;
        self.verified_fields = None;
        self.phase = Phase::Idle;
        self.hasher = None;
        self.download_retries = 0;
    }

    fn install_window_open(&self, ctx: &OtaTickContext) -> bool {
        if !ctx.paired || !ctx.ring_empty {
            return false;
        }
        if ctx.in_pairing || ctx.in_captive_portal || ctx.factory_reset_hold {
            return false;
        }
        let now = self.clock.now_ms();
        if now.saturating_sub(ctx.last_activity_ms) < INSTALL_WINDOW_IDLE_MS {
            return false;
        }
        if self.battery_powered {
            let ps = self.power.read();
            if !ps.vbus_present {
                match ps.battery_pct {
                    Some(pct) if pct >= MIN_BATTERY_PCT_STICKS3 => {}
                    _ => return false,
                }
            }
        }
        match ctx.wifi_rssi_dbm {
            Some(rssi) if rssi >= MIN_WIFI_RSSI_DBM => {}
            _ => return false,
        }
        true
    }

    fn render_updating(&mut self) {
        let bands = DisplayBands {
            band1: "UPDATING".to_owned(),
            band2_line1: "Updating, do".to_owned(),
            band2_line2: "not unplug".to_owned(),
            band3: match self.progress_pct() {
                Some(p) => format!("{p}%"),
                None => String::new(),
            },
            ..DisplayBands::default()
        };
        self.display.render(&bands);
    }

    fn should_suppress(&self, release_id: &str) -> bool {
        !self.suppress_release_id.is_empty() && self.suppress_release_id == release_id
    }

    fn consume_checkin_timeout(&mut self) -> Option<OtaHeartbeatFields> {
        if self.phase != Phase::PendingVerify {
            return None;
        }
        let now = self.clock.now_ms();
        if now.saturating_sub(self.boot_ms) < CHECKIN_DEADLINE_MS {
            return None;
        }
        self.nvs.stockbot_set_str(
            ota_nvs_keys::OTA_ERR,
            DeviceOtaError::CheckinTimeout.as_str(),
        );
        let _ = self.slots.mark_running_invalid_and_reboot();
        None
    }

    fn emit_rollback_once(&mut self) {
        if self.rollback_reported {
            self.pending_rollback_report = false;
            return;
        }
        let rel = self
            .nvs
            .stockbot_get_str(ota_nvs_keys::OTA_REL)
            .unwrap_or_default();
        if rel.is_empty() {
            self.pending_rollback_report = false;
            return;
        }
        self.pending_rollback_report = false;
        self.rollback_reported = true;
        let err = self
            .nvs
            .stockbot_get_str(ota_nvs_keys::OTA_ERR)
            .unwrap_or_else(|| DeviceOtaError::BootFailed.as_str().to_owned());
        self.nvs.stockbot_set_str(ota_nvs_keys::OTA_SUPP, &rel);
        self.suppress_release_id = rel.clone();
        self.nvs.stockbot_remove(ota_nvs_keys::OTA_REL);
        self.nvs.stockbot_remove(ota_nvs_keys::OTA_ERR);
        self.one_shot = Some(OtaHeartbeatFields {
            ota_state: "rolled_back",
            ota_release_id: Some(rel),
            ota_error: Some(map_stored_ota_err(&err)),
            ota_progress_pct: None,
            rollback: true,
        });
    }
}

fn map_stored_ota_err(s: &str) -> &'static str {
    match s {
        "wrong_target" => "wrong_target",
        "unknown_key" => "unknown_key",
        "bad_signature" => "bad_signature",
        "too_large" => "too_large",
        "download_failed" => "download_failed",
        "http_status" => "http_status",
        "bad_size" => "bad_size",
        "bad_sha256" => "bad_sha256",
        "flash_write_failed" => "flash_write_failed",
        "image_invalid" => "image_invalid",
        "checkin_timeout" => "checkin_timeout",
        _ => "boot_failed",
    }
}

fn hex_lower(digest: &[u8]) -> String {
    let mut s = String::with_capacity(digest.len() * 2);
    for b in digest {
        use std::fmt::Write;
        let _ = write!(s, "{b:02x}");
    }
    s
}
