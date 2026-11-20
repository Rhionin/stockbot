//! NUL-terminated `stockbot-fw-target:<id>` baked into the app image for CI verification.

#[cfg(feature = "sticks3_u173")]
#[used]
static STOCKBOT_FW_TARGET_MARKER: [u8; 32] = *b"stockbot-fw-target:sticks3_u173\0";

#[cfg(feature = "sticks3_u173_uart")]
#[used]
static STOCKBOT_FW_TARGET_MARKER: [u8; 37] = *b"stockbot-fw-target:sticks3_u173_uart\0";

#[cfg(feature = "atoms3lite_u173_bench")]
#[used]
static STOCKBOT_FW_TARGET_MARKER: [u8; 42] = *b"stockbot-fw-target:atoms3lite_u173_bench\0";

#[cfg(feature = "atoms3lite_symcode_hid")]
#[used]
static STOCKBOT_FW_TARGET_MARKER: [u8; 42] = *b"stockbot-fw-target:atoms3lite_symcode_hid\0";

#[cfg(not(any(
    feature = "sticks3_u173",
    feature = "sticks3_u173_uart",
    feature = "atoms3lite_u173_bench",
    feature = "atoms3lite_symcode_hid"
)))]
compile_error!("Enable exactly one target feature (sticks3_u173, sticks3_u173_uart, atoms3lite_u173_bench, atoms3lite_symcode_hid)");