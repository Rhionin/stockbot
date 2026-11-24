use stockbot_scanner_core::{
    accept_with_duplicate_cooldown, classify_barcode, is_pairing_code, BarcodeKind, Direction,
    DuplicateCooldownState, OperationalState, RESCAN_COOLDOWN_MS,
};

/// Barcode classification: product, control, pairing code and setup payload by operational state.
#[test]
fn barcode_filter() {
    // URLs are not 1D barcodes
    assert_eq!(
        classify_barcode("https://example.com", OperationalState::Paired),
        BarcodeKind::RejectedNon1D
    );
    // URLs are not 1D barcodes (http)
    assert_eq!(
        classify_barcode("http://foo", OperationalState::Paired),
        BarcodeKind::RejectedNon1D
    );
    // JSON that is not in setup state is rejected
    assert_eq!(
        classify_barcode(r#"{"v":1,"ssid":"x"}"#, OperationalState::Paired),
        BarcodeKind::RejectedNon1D
    );
    // alphanumeric text is not a product barcode
    assert_eq!(
        classify_barcode("abc123", OperationalState::Paired),
        BarcodeKind::RejectedNon1D
    );
    // empty input is rejected
    assert_eq!(
        classify_barcode("", OperationalState::Paired),
        BarcodeKind::RejectedNon1D
    );
    // 15 digits is too long for a product barcode
    assert_eq!(
        classify_barcode("123456789012345", OperationalState::Paired),
        BarcodeKind::RejectedNon1D
    );

    // 12-digit UPC is a product
    assert_eq!(
        classify_barcode("012345678905", OperationalState::Paired),
        BarcodeKind::Product
    );
    // short numeric codes (4 digits) are products
    assert_eq!(
        classify_barcode("4011", OperationalState::Paired),
        BarcodeKind::Product
    );
    // 14 digits is the longest accepted product barcode
    assert_eq!(
        classify_barcode("12345678901234", OperationalState::Paired),
        BarcodeKind::Product
    );

    // STOCK-IN control barcode (hyphen form)
    assert_eq!(
        classify_barcode("STOCK-IN", OperationalState::Paired),
        BarcodeKind::ControlStockIn
    );
    // STOCK-IN control barcode is case-insensitive and accepts an underscore
    assert_eq!(
        classify_barcode("stock_in", OperationalState::Paired),
        BarcodeKind::ControlStockIn
    );
    // STOCK-OUT control barcode (hyphen form)
    assert_eq!(
        classify_barcode("STOCK-OUT", OperationalState::Paired),
        BarcodeKind::ControlStockOut
    );
    // STOCK-OUT control barcode (underscore form)
    assert_eq!(
        classify_barcode("STOCK_OUT", OperationalState::Paired),
        BarcodeKind::ControlStockOut
    );

    // valid pairing code shape and check character
    assert!(is_pairing_code("SBP-0123-ABCD-G"));
    // valid pairing code with check character U
    assert!(is_pairing_code("SBP-0000-0014-U"));
    // wrong check character is not a pairing code
    assert!(!is_pairing_code("SBP-0000-0014-G"));
    // a check character that does not match the payload is rejected
    assert!(!is_pairing_code("SBP-0123-ABCD-0"));
    // pairing codes are recognised only while pairing
    assert_eq!(
        classify_barcode("SBP-0123-ABCD-G", OperationalState::Pairing),
        BarcodeKind::PairingCode
    );
    // pairing codes are rejected once paired
    assert_eq!(
        classify_barcode("SBP-0123-ABCD-G", OperationalState::Paired),
        BarcodeKind::RejectedNon1D
    );

    // setup payload JSON is accepted while unprovisioned
    assert_eq!(
        classify_barcode(r#"{"v":1,"ssid":"Home"}"#, OperationalState::Unprovisioned),
        BarcodeKind::SetupPayload
    );
    // setup payload JSON is rejected while pairing
    assert_eq!(
        classify_barcode(r#"{"v":1,"ssid":"Home"}"#, OperationalState::Pairing),
        BarcodeKind::RejectedNon1D
    );
}

/// Duplicate-scan cooldown for repeated barcodes in the same direction.
#[test]
fn duplicate_cooldown() {
    let mut state = DuplicateCooldownState::default();
    // first scan is accepted
    assert!(accept_with_duplicate_cooldown(
        &mut state,
        "4011",
        Direction::StockOut,
        1000,
        false
    ));
    // same barcode and direction 1999 ms later is a duplicate
    assert!(!accept_with_duplicate_cooldown(
        &mut state,
        "4011",
        Direction::StockOut,
        2999,
        false
    ));
    // same barcode accepted once the 2000 ms cooldown elapses
    assert!(accept_with_duplicate_cooldown(
        &mut state,
        "4011",
        Direction::StockOut,
        3000,
        false
    ));
    // a different barcode is accepted inside the cooldown
    assert!(accept_with_duplicate_cooldown(
        &mut state,
        "4012",
        Direction::StockOut,
        3100,
        false
    ));
    // same barcode in the other direction is accepted
    assert!(accept_with_duplicate_cooldown(
        &mut state,
        "4011",
        Direction::StockIn,
        3200,
        false
    ));
    // forced accept bypasses the cooldown
    assert!(accept_with_duplicate_cooldown(
        &mut state,
        "4011",
        Direction::StockOut,
        3300,
        true
    ));
}

/// Cooldown boundary: 1999 ms after last accept is rejected; 2000 ms is accepted (`RESCAN_COOLDOWN_MS`).
#[test]
fn duplicate_cooldown_boundary_ms() {
    let mut state = DuplicateCooldownState::default();
    let t0 = 10_000u64;
    assert!(accept_with_duplicate_cooldown(
        &mut state,
        "4011",
        Direction::StockOut,
        t0,
        false
    ));
    assert!(!accept_with_duplicate_cooldown(
        &mut state,
        "4011",
        Direction::StockOut,
        t0 + RESCAN_COOLDOWN_MS - 1,
        false
    ));
    assert!(accept_with_duplicate_cooldown(
        &mut state,
        "4011",
        Direction::StockOut,
        t0 + RESCAN_COOLDOWN_MS,
        false
    ));
}

/// Direction change is always accepted inside the cooldown window.
#[test]
fn duplicate_cooldown_direction_change_always_accepted() {
    let mut state = DuplicateCooldownState::default();
    assert!(accept_with_duplicate_cooldown(
        &mut state,
        "4011",
        Direction::StockOut,
        1000,
        false
    ));
    assert!(accept_with_duplicate_cooldown(
        &mut state,
        "4011",
        Direction::StockIn,
        1001,
        false
    ));
}

/// Widened `millis()` in `u64`: when `now` is before `last`, wrapping subtraction accepts.
#[test]
fn duplicate_cooldown_uint64_wrap_accepted_after_millis_carry() {
    let mut state = DuplicateCooldownState::default();
    assert!(accept_with_duplicate_cooldown(
        &mut state,
        "4011",
        Direction::StockOut,
        4_294_967_000,
        false
    ));
    assert!(accept_with_duplicate_cooldown(
        &mut state,
        "4011",
        Direction::StockOut,
        100,
        false
    ));
}
