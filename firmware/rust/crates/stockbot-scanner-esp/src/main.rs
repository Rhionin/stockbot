//! ESP32-S3 scanner firmware entry (S7: boot + log only; peripherals in S9).

#![forbid(unsafe_code)]

mod marker;

fn selected_target_id() -> &'static str {
    if cfg!(feature = "sticks3_u173") {
        "sticks3_u173"
    } else if cfg!(feature = "sticks3_u173_uart") {
        "sticks3_u173_uart"
    } else if cfg!(feature = "atoms3lite_u173_bench") {
        "atoms3lite_u173_bench"
    } else if cfg!(feature = "atoms3lite_symcode_hid") {
        "atoms3lite_symcode_hid"
    } else {
        "unknown"
    }
}

fn main() {
    esp_idf_svc::sys::link_patches();
    esp_idf_svc::log::EspLogger::initialize_default();

    // Keep the rodata marker through LTO even without `no_mangle`.
    std::hint::black_box(marker::embedded_bytes());

    let target = selected_target_id();
    log::info!(
        "stockbot-scanner-esp boot (slice S7); target={target}; marker=stockbot-fw-target:{target}"
    );

    loop {
        std::thread::sleep(std::time::Duration::from_secs(10));
        log::info!("stockbot-scanner-esp idle (target={target})");
    }
}