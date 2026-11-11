use std::collections::HashSet;

use stockbot_scanner_core::pairing_check_character;

const PAYLOAD_ALPHABET: &str = "0123456789ABCDEFGHJKMNPQRSTVWXYZ";
const CHECK_ALPHABET: &str = "0123456789ABCDEFGHJKMNPQRSTVWXYZ*~$=U";

#[test]
fn pairing_check_character_accepts_every_payload_symbol() {
    for ch in PAYLOAD_ALPHABET.chars() {
        let payload = ch.to_string().repeat(8);
        assert!(
            pairing_check_character(&payload).is_some(),
            "payload symbol {:?}",
            ch
        );
    }
}

#[test]
fn pairing_check_character_can_emit_every_check_symbol() {
    let mut seen = HashSet::new();
    for a in PAYLOAD_ALPHABET.chars() {
        for b in PAYLOAD_ALPHABET.chars() {
            let payload = format!("{a}{a}{a}{a}{b}{b}{b}{b}");
            if let Some(check) = pairing_check_character(&payload) {
                seen.insert(check);
            }
        }
    }
    for ch in CHECK_ALPHABET.chars() {
        assert!(seen.contains(&ch), "missing check symbol {:?}", ch);
    }
}

#[test]
fn pairing_check_character_long_input_does_not_panic() {
    let long = "0".repeat(64);
    assert!(pairing_check_character(&long).is_some());
}
