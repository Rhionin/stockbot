use stockbot_scanner_core::{
    accept_with_duplicate_cooldown, classify_barcode, is_pairing_code, BarcodeKind, Direction,
    DuplicateCooldownState, OperationalState, RESCAN_COOLDOWN_MS,
};

/// Ports `test_barcode_filter` from firmware/test/test_scanner_core/test_main.cpp
#[test]
fn cpp_test_barcode_filter() {
    // test_main.cpp:76
    assert_eq!(
        classify_barcode("https://example.com", OperationalState::Paired),
        BarcodeKind::RejectedNon1D
    );
    // test_main.cpp:77
    assert_eq!(
        classify_barcode("http://foo", OperationalState::Paired),
        BarcodeKind::RejectedNon1D
    );
    // test_main.cpp:78
    assert_eq!(
        classify_barcode(r#"{"v":1,"ssid":"x"}"#, OperationalState::Paired),
        BarcodeKind::RejectedNon1D
    );
    // test_main.cpp:79
    assert_eq!(
        classify_barcode("abc123", OperationalState::Paired),
        BarcodeKind::RejectedNon1D
    );
    // test_main.cpp:80
    assert_eq!(
        classify_barcode("", OperationalState::Paired),
        BarcodeKind::RejectedNon1D
    );
    // test_main.cpp:81
    assert_eq!(
        classify_barcode("123456789012345", OperationalState::Paired),
        BarcodeKind::RejectedNon1D
    );

    // test_main.cpp:83
    assert_eq!(
        classify_barcode("012345678905", OperationalState::Paired),
        BarcodeKind::Product
    );
    // test_main.cpp:84
    assert_eq!(
        classify_barcode("4011", OperationalState::Paired),
        BarcodeKind::Product
    );
    // test_main.cpp:85
    assert_eq!(
        classify_barcode("12345678901234", OperationalState::Paired),
        BarcodeKind::Product
    );

    // test_main.cpp:87
    assert_eq!(
        classify_barcode("STOCK-IN", OperationalState::Paired),
        BarcodeKind::ControlStockIn
    );
    // test_main.cpp:88
    assert_eq!(
        classify_barcode("stock_in", OperationalState::Paired),
        BarcodeKind::ControlStockIn
    );
    // test_main.cpp:89
    assert_eq!(
        classify_barcode("STOCK-OUT", OperationalState::Paired),
        BarcodeKind::ControlStockOut
    );
    // test_main.cpp:90
    assert_eq!(
        classify_barcode("STOCK_OUT", OperationalState::Paired),
        BarcodeKind::ControlStockOut
    );

    // test_main.cpp:92
    assert!(is_pairing_code("SBP-0123-ABCD-G"));
    // test_main.cpp:93
    assert!(is_pairing_code("SBP-0000-0014-U"));
    // test_main.cpp:94
    assert!(!is_pairing_code("SBP-0000-0014-G"));
    // test_main.cpp:95
    assert!(!is_pairing_code("SBP-0123-ABCD-0"));
    // test_main.cpp:96
    assert_eq!(
        classify_barcode("SBP-0123-ABCD-G", OperationalState::Pairing),
        BarcodeKind::PairingCode
    );
    // test_main.cpp:97
    assert_eq!(
        classify_barcode("SBP-0123-ABCD-G", OperationalState::Paired),
        BarcodeKind::RejectedNon1D
    );

    // test_main.cpp:99-100
    assert_eq!(
        classify_barcode(r#"{"v":1,"ssid":"Home"}"#, OperationalState::Unprovisioned),
        BarcodeKind::SetupPayload
    );
    // test_main.cpp:101
    assert_eq!(
        classify_barcode(r#"{"v":1,"ssid":"Home"}"#, OperationalState::Pairing),
        BarcodeKind::RejectedNon1D
    );
}

/// Ports `test_duplicate_cooldown` from firmware/test/test_scanner_core/test_main.cpp
#[test]
fn cpp_test_duplicate_cooldown() {
    let mut state = DuplicateCooldownState::default();
    // test_main.cpp:110
    assert!(accept_with_duplicate_cooldown(
        &mut state,
        "4011",
        Direction::StockOut,
        1000,
        false
    ));
    // test_main.cpp:111
    assert!(!accept_with_duplicate_cooldown(
        &mut state,
        "4011",
        Direction::StockOut,
        2999,
        false
    ));
    // test_main.cpp:112
    assert!(accept_with_duplicate_cooldown(
        &mut state,
        "4011",
        Direction::StockOut,
        3000,
        false
    ));
    // test_main.cpp:113
    assert!(accept_with_duplicate_cooldown(
        &mut state,
        "4012",
        Direction::StockOut,
        3100,
        false
    ));
    // test_main.cpp:114
    assert!(accept_with_duplicate_cooldown(
        &mut state,
        "4011",
        Direction::StockIn,
        3200,
        false
    ));
    // test_main.cpp:115
    assert!(accept_with_duplicate_cooldown(
        &mut state,
        "4011",
        Direction::StockOut,
        3300,
        true
    ));
}

/// Cooldown boundary: 1999 ms after last accept is rejected; 2000 ms is accepted (C++ RESCAN_COOLDOWN_MS).
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

/// `millis()` wrap: elapsed 2000 ms via `u32::wrapping_sub`, so the rescan is accepted.
#[test]
fn duplicate_cooldown_millis_wrap_accepted_at_2000ms() {
    let last = u64::from(u32::MAX - 500);
    let mut state = DuplicateCooldownState {
        last_barcode: "4011".into(),
        last_direction: Direction::StockOut,
        last_accept_ms: last,
    };
    assert!(accept_with_duplicate_cooldown(
        &mut state,
        "4011",
        Direction::StockOut,
        1499,
        false
    ));
}

/// `millis()` wrap: elapsed 1999 ms, below cooldown.
#[test]
fn duplicate_cooldown_millis_wrap_rejected_at_1999ms() {
    let last = u64::from(u32::MAX - 500);
    let mut state = DuplicateCooldownState {
        last_barcode: "4011".into(),
        last_direction: Direction::StockOut,
        last_accept_ms: last,
    };
    assert!(!accept_with_duplicate_cooldown(
        &mut state,
        "4011",
        Direction::StockOut,
        1498,
        false
    ));
}

/// Backwards linear gap still accepts, matching C++ unsigned subtraction.
#[test]
fn duplicate_cooldown_backwards_gap_accepted_like_cpp() {
    let mut state = DuplicateCooldownState {
        last_barcode: "4011".into(),
        last_direction: Direction::StockOut,
        last_accept_ms: 10_000,
    };
    assert!(accept_with_duplicate_cooldown(
        &mut state,
        "4011",
        Direction::StockOut,
        5_000,
        false
    ));
}
