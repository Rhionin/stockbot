//! Captive portal (#745): setup form decoding and checks, the HTML page and the catch-all DNS answer.
//!
//! Each refusal test starts from a body that saves (see `good_body_saves`) and breaks one field,
//! so removing the check it covers makes the test fail.

use stockbot_scanner_core::{
    build_dns_answer, decode_setup_form, parse_setup_form, render_setup_page,
    render_setup_saved_page, CaptivePortalSubmission, SetupFormError, SetupFormValues,
    DEFAULT_SERVER_URL, MAX_SETUP_FORM_BYTES, PORTAL_IPV4,
};

const GOOD_CODE: &str = "SBP-0123-ABCD-G";

fn body(ssid: &str, pass: &str, url: &str, code: &str) -> String {
    format!("ssid={ssid}&pass={pass}&url={url}&code={code}")
}

fn good_with(field: &str, value: &str) -> String {
    let mut fields = [
        ("ssid", "HomeNet".to_owned()),
        ("pass", "secret-pass".to_owned()),
        ("url", "https%3A%2F%2Fstockbot.rhionin.com".to_owned()),
        ("code", GOOD_CODE.to_owned()),
    ];
    for (name, slot) in &mut fields {
        if *name == field {
            *slot = value.to_owned();
        }
    }
    body(&fields[0].1, &fields[1].1, &fields[2].1, &fields[3].1)
}

#[test]
fn good_body_saves() {
    let got = parse_setup_form(good_with("", "").as_bytes()).expect("saves");
    assert_eq!(
        got,
        CaptivePortalSubmission {
            ready: true,
            wifi_ssid: "HomeNet".into(),
            wifi_pass: "secret-pass".into(),
            server_url: "https://stockbot.rhionin.com".into(),
            pairing_code: GOOD_CODE.into(),
        }
    );
}

#[test]
fn empty_wifi_name_is_refused() {
    for ssid in ["", "+", "%20%20"] {
        assert_eq!(
            parse_setup_form(good_with("ssid", ssid).as_bytes()),
            Err(SetupFormError::WifiNameMissing),
            "ssid={ssid:?}"
        );
    }
    let no_field = format!("pass=secret-pass&code={GOOD_CODE}");
    assert_eq!(
        parse_setup_form(no_field.as_bytes()),
        Err(SetupFormError::WifiNameMissing)
    );
}

#[test]
fn bad_server_url_is_refused() {
    for url in [
        "stockbot.rhionin.com",
        "ftp%3A%2F%2Fstockbot.rhionin.com",
        "https%3A%2F%2F",
        "https%3A%2F%2F%2Fpath",
        "http%3A%2F%2F%3A8080",
        "https%3A%2F%2Fstock+bot.example",
        "http%3A%2F%2Fpi%3A",
        "http%3A%2F%2Fpi%3A99999",
        "http%3A%2F%2Fpi%3Aabc",
    ] {
        assert_eq!(
            parse_setup_form(good_with("url", url).as_bytes()),
            Err(SetupFormError::ServerUrlInvalid),
            "url={url:?}"
        );
    }
}

#[test]
fn server_url_forms_that_save() {
    for (url, want) in [
        ("", DEFAULT_SERVER_URL),
        ("+", DEFAULT_SERVER_URL),
        (
            "http%3A%2F%2F192.168.1.10%3A8080%2F",
            "http://192.168.1.10:8080",
        ),
        ("HTTPS%3A%2F%2Fexample.com", "HTTPS://example.com"),
        ("+https%3A%2F%2Fexample.com+", "https://example.com"),
    ] {
        let got = parse_setup_form(good_with("url", url).as_bytes()).expect(url);
        assert_eq!(got.server_url, want, "url={url:?}");
    }
    let no_field = format!("ssid=HomeNet&pass=secret-pass&code={GOOD_CODE}");
    assert_eq!(
        parse_setup_form(no_field.as_bytes()).unwrap().server_url,
        DEFAULT_SERVER_URL
    );
}

#[test]
fn bad_pairing_code_is_refused() {
    for code in [
        "SBP-0123-ABCD-H",
        "SBP-0123-ABCD",
        "0123-ABCD-G",
        "hello",
        "SBP-0123-ABCD-G-SBP-0123-ABCD-G-X",
    ] {
        assert_eq!(
            parse_setup_form(good_with("code", code).as_bytes()),
            Err(SetupFormError::PairingCodeInvalid),
            "code={code:?}"
        );
    }
}

#[test]
fn pairing_code_is_optional_and_normalized() {
    let got = parse_setup_form(good_with("code", "").as_bytes()).unwrap();
    assert_eq!(got.pairing_code, "");
    let got = parse_setup_form(good_with("code", "+sbp-0123-abcd-g+").as_bytes()).unwrap();
    assert_eq!(got.pairing_code, GOOD_CODE);
}

#[test]
fn long_values_are_refused() {
    let ssid_32 = "a".repeat(32);
    assert!(parse_setup_form(good_with("ssid", &ssid_32).as_bytes()).is_ok());
    assert_eq!(
        parse_setup_form(good_with("ssid", &"a".repeat(33)).as_bytes()),
        Err(SetupFormError::WifiNameTooLong)
    );
    // Eleven 3-byte characters: 11 characters but 33 bytes.
    let wide = "%E2%82%AC".repeat(11);
    assert_eq!(
        parse_setup_form(good_with("ssid", &wide).as_bytes()),
        Err(SetupFormError::WifiNameTooLong)
    );

    assert!(parse_setup_form(good_with("pass", &"p".repeat(64)).as_bytes()).is_ok());
    assert_eq!(
        parse_setup_form(good_with("pass", &"p".repeat(65)).as_bytes()),
        Err(SetupFormError::WifiPasswordLength)
    );

    let long_url = format!("https%3A%2F%2F{}.example", "a".repeat(190));
    assert_eq!(
        parse_setup_form(good_with("url", &long_url).as_bytes()),
        Err(SetupFormError::ServerUrlTooLong)
    );

    let mut huge = good_with("", "");
    huge.push_str("&pad=");
    huge.push_str(&"x".repeat(MAX_SETUP_FORM_BYTES));
    assert_eq!(
        parse_setup_form(huge.as_bytes()),
        Err(SetupFormError::FormTooLong)
    );
}

#[test]
fn short_password_is_refused_and_empty_means_open_network() {
    assert_eq!(
        parse_setup_form(good_with("pass", "1234567").as_bytes()),
        Err(SetupFormError::WifiPasswordLength)
    );
    assert!(parse_setup_form(good_with("pass", "12345678").as_bytes()).is_ok());
    let open = parse_setup_form(good_with("pass", "").as_bytes()).unwrap();
    assert_eq!(open.wifi_pass, "");
}

#[test]
fn url_encoded_characters_are_decoded() {
    let got = parse_setup_form(
        good_with("ssid", "Caf%C3%A9+%26+Bar%3D1%2b2")
            .replace("pass=secret-pass", "pass=p%25ss+w%6Frd%21")
            .as_bytes(),
    )
    .unwrap();
    assert_eq!(got.wifi_ssid, "Café & Bar=1+2");
    assert_eq!(got.wifi_pass, "p%ss word!");
}

#[test]
fn broken_url_encoding_is_refused() {
    for ssid in ["Home%", "Home%2", "Home%zz", "Home%FF", "%C3"] {
        assert_eq!(
            parse_setup_form(good_with("ssid", ssid).as_bytes()),
            Err(SetupFormError::BadEncoding),
            "ssid={ssid:?}"
        );
    }
}

#[test]
fn unknown_and_repeated_fields() {
    let got = parse_setup_form(
        format!("x=1&&ssid=First&ssid=Second&pass=secret-pass&flag&code={GOOD_CODE}").as_bytes(),
    )
    .unwrap();
    assert_eq!(got.wifi_ssid, "Second");
}

#[test]
fn every_error_has_plain_text() {
    for err in [
        SetupFormError::FormTooLong,
        SetupFormError::BadEncoding,
        SetupFormError::WifiNameMissing,
        SetupFormError::WifiNameTooLong,
        SetupFormError::WifiPasswordLength,
        SetupFormError::ServerUrlTooLong,
        SetupFormError::ServerUrlInvalid,
        SetupFormError::PairingCodeInvalid,
    ] {
        let text = err.message();
        assert!(text.ends_with('.'), "{err:?}: {text}");
        assert!(!text.contains("::"), "{err:?}: {text}");
    }
}

#[test]
fn setup_page_escapes_values_and_never_echoes_the_password() {
    let values = decode_setup_form(
        b"ssid=%22%3E%3Cscript%3Ex%3C%2Fscript%3E&pass=hunter2-secret&url=&code=a%27b",
    )
    .unwrap();
    let page = render_setup_page(&values, Some(SetupFormError::PairingCodeInvalid));
    assert!(!page.contains("<script>"));
    assert!(page.contains("&quot;&gt;&lt;script&gt;x&lt;/script&gt;"));
    assert!(page.contains("value=\"a&#39;b\""));
    assert!(!page.contains("hunter2-secret"));
    assert!(page.contains(SetupFormError::PairingCodeInvalid.message()));
    assert!(page.contains(&format!("value=\"{DEFAULT_SERVER_URL}\"")));
    for name in ["ssid", "pass", "url", "code"] {
        assert!(page.contains(&format!("name=\"{name}\"")), "{name}");
    }
}

#[test]
fn fresh_setup_page_has_default_server_and_no_error() {
    let page = render_setup_page(&SetupFormValues::default(), None);
    assert!(page.contains(&format!("value=\"{DEFAULT_SERVER_URL}\"")));
    assert!(!page.contains("class=\"error\""));
    assert!(page.contains("method=\"post\""));
}

#[test]
fn saved_page_names_the_network_escaped() {
    let page = render_setup_saved_page("Tom & Jo's <Wi-Fi>");
    assert!(page.contains("Tom &amp; Jo&#39;s &lt;Wi-Fi&gt;"));
}

fn dns_query(id: u16, flags: u16, name: &str, qtype: u16, extra: &[u8]) -> Vec<u8> {
    let mut q = Vec::new();
    q.extend_from_slice(&id.to_be_bytes());
    q.extend_from_slice(&flags.to_be_bytes());
    q.extend_from_slice(&1u16.to_be_bytes());
    q.extend_from_slice(&0u16.to_be_bytes());
    q.extend_from_slice(&0u16.to_be_bytes());
    q.extend_from_slice(&u16::from(!extra.is_empty()).to_be_bytes());
    for label in name.split('.') {
        q.push(label.len() as u8);
        q.extend_from_slice(label.as_bytes());
    }
    q.push(0);
    q.extend_from_slice(&qtype.to_be_bytes());
    q.extend_from_slice(&1u16.to_be_bytes());
    q.extend_from_slice(extra);
    q
}

#[test]
fn dns_a_query_points_at_the_portal() {
    let query = dns_query(0xBEEF, 0x0100, "connectivitycheck.gstatic.com", 1, &[]);
    let answer = build_dns_answer(&query, PORTAL_IPV4).expect("answer");
    assert_eq!(&answer[0..2], &[0xBE, 0xEF]);
    assert_eq!(
        &answer[2..4],
        &[0x85, 0x00],
        "response, authoritative, RD echoed"
    );
    assert_eq!(&answer[4..12], &[0, 1, 0, 1, 0, 0, 0, 0]);
    assert_eq!(&answer[12..query.len()], &query[12..]);
    let rr = &answer[query.len()..];
    assert_eq!(
        rr,
        &[0xC0, 0x0C, 0, 1, 0, 1, 0, 0, 0, 60, 0, 4, 192, 168, 4, 1]
    );
}

#[test]
fn dns_drops_trailing_records_and_answers_aaaa_empty() {
    let opt = [0, 0, 41, 0x10, 0, 0, 0, 0, 0, 0, 0];
    let query = dns_query(7, 0x0000, "captive.apple.com", 1, &opt);
    let answer = build_dns_answer(&query, PORTAL_IPV4).unwrap();
    assert_eq!(&answer[2..4], &[0x84, 0x00]);
    assert_eq!(&answer[10..12], &[0, 0], "no additional records");
    assert_eq!(answer.len(), query.len() - opt.len() + 16);
    assert_eq!(&answer[answer.len() - 4..], &PORTAL_IPV4);

    let aaaa = dns_query(8, 0x0100, "captive.apple.com", 28, &[]);
    let answer = build_dns_answer(&aaaa, PORTAL_IPV4).unwrap();
    assert_eq!(&answer[6..8], &[0, 0], "no answers");
    assert_eq!(answer.len(), aaaa.len());
}

#[test]
fn dns_ignores_what_is_not_a_plain_query() {
    let query = dns_query(1, 0x0100, "example.com", 1, &[]);
    assert!(
        build_dns_answer(&query[..11], PORTAL_IPV4).is_none(),
        "short header"
    );
    assert!(
        build_dns_answer(&query[..query.len() - 1], PORTAL_IPV4).is_none(),
        "cut question"
    );
    let mut response = query.clone();
    response[2] |= 0x80;
    assert!(
        build_dns_answer(&response, PORTAL_IPV4).is_none(),
        "a response"
    );
    let mut status = query.clone();
    status[2] |= 0x10;
    assert!(
        build_dns_answer(&status, PORTAL_IPV4).is_none(),
        "not opcode 0"
    );
    let mut two = query.clone();
    two[5] = 2;
    assert!(
        build_dns_answer(&two, PORTAL_IPV4).is_none(),
        "two questions"
    );
    let mut pointer = query.clone();
    pointer[12] = 0xC0;
    assert!(
        build_dns_answer(&pointer, PORTAL_IPV4).is_none(),
        "compressed name"
    );
    assert!(
        build_dns_answer(&[0u8; 600], PORTAL_IPV4).is_none(),
        "oversized"
    );
}
