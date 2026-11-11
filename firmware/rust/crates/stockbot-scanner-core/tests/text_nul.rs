use stockbot_scanner_core::{
    classify_barcode, is_ascii_digit_product_barcode, normalize_scan, trim_ascii, BarcodeKind,
    OperationalState,
};

#[test]
fn trim_ascii_strips_leading_and_trailing_nul() {
    let raw = format!("\u{0}{}\u{0}", "4011");
    assert_eq!(trim_ascii(&raw), "4011");
}

#[test]
fn product_barcode_after_nul_trim() {
    let raw = format!("\u{0}{}\u{0}", "4011");
    assert!(is_ascii_digit_product_barcode(&raw));
}

#[test]
fn classify_product_with_nul_padding() {
    let raw = format!("\u{0}{}\u{0}", "4011");
    assert_eq!(
        classify_barcode(&raw, OperationalState::Paired),
        BarcodeKind::Product
    );
}

#[test]
fn interior_nul_rejects_normalize_and_product() {
    let raw = "40\u{0}11";
    assert!(normalize_scan(raw).is_none());
    assert!(!is_ascii_digit_product_barcode(raw));
    assert_eq!(
        classify_barcode(raw, OperationalState::Paired),
        BarcodeKind::RejectedNon1D
    );
}
