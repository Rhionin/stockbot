//! Firmware OTA target ids and the embedded `stockbot-fw-target:<id>` marker (issue #445 S7).

/// Target ids from [firmware-ota.md](https://github.com/Rhionin/stockbot/blob/master/docs/design/firmware-ota.md) §2.
pub const TARGET_IDS: [&str; 4] = [
    "sticks3_u173",
    "sticks3_u173_uart",
    "atoms3lite_u173_bench",
    "atoms3lite_symcode_hid",
];

const MARKER_PREFIX: &str = "stockbot-fw-target:";

/// Returns the NUL-terminated marker bytes CI greps for in the app image.
pub fn marker_with_nul(target_id: &str) -> Option<Vec<u8>> {
    if !TARGET_IDS.contains(&target_id) {
        return None;
    }
    let mut out = String::with_capacity(MARKER_PREFIX.len() + target_id.len() + 1);
    out.push_str(MARKER_PREFIX);
    out.push_str(target_id);
    out.push('\0');
    Some(out.into_bytes())
}

/// Full marker string without the trailing NUL (for logging).
pub fn marker_text(target_id: &str) -> Option<String> {
    TARGET_IDS
        .contains(&target_id)
        .then(|| format!("{MARKER_PREFIX}{target_id}"))
}
