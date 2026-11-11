use std::collections::HashSet;

use stockbot_scanner_core::{is_pairing_code, pairing_check_character};

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

/// Long inputs overflow a plain `u64` accumulate; C++ host oracles: `1`×64 → `V`, `Z`×64 → `B`.
#[test]
fn pairing_check_character_long_overflow_inputs_match_cpp() {
    let cases = [("1".repeat(64), 'V'), ("Z".repeat(64), 'B')];
    for (payload, want) in cases {
        assert_eq!(
            pairing_check_character(&payload).expect("64-symbol payload"),
            want,
            "payload first char {:?}",
            payload.chars().next()
        );
    }
}

/// Full `SBP-` codes with a correct check char; every payload alphabet symbol appears in grammar + validation.
#[test]
fn is_pairing_code_accepts_sbp_for_every_payload_symbol() {
    for ch in PAYLOAD_ALPHABET.chars() {
        let body = format!("{ch}0000000");
        let check = pairing_check_character(&body).expect("8-char payload");
        let code = format!("SBP-{}-{}-{}", &body[0..4], &body[4..8], check);
        assert!(is_pairing_code(&code), "payload symbol {:?} in {code}", ch);
    }
}
