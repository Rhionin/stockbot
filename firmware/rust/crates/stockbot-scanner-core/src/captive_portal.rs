//! Wi-Fi setup page logic for the captive portal (#745): the form, its checks, the HTML and the
//! catch-all DNS answer. The ESP side only moves bytes between the radio and these functions.

use crate::barcode::is_pairing_code;
use crate::scanner_ports::CaptivePortalSubmission;
use crate::text::normalize_scan;
use crate::transport::transport_for_url;

/// The SoftAP gateway; every DNS answer points here so the phone opens the setup page.
pub const PORTAL_IPV4: [u8; 4] = [192, 168, 4, 1];
pub const DEFAULT_SERVER_URL: &str = "https://stockbot.rhionin.com";

/// Largest form body accepted; the four fields at their limits fit with room for encoding.
pub const MAX_SETUP_FORM_BYTES: usize = 2048;
/// 802.11 SSIDs are at most 32 bytes.
pub const MAX_WIFI_NAME_BYTES: usize = 32;
/// WPA2 takes an 8 to 63 character passphrase or a 64 hex digit key.
pub const MIN_WIFI_PASSWORD_BYTES: usize = 8;
pub const MAX_WIFI_PASSWORD_BYTES: usize = 64;
pub const MAX_SERVER_URL_BYTES: usize = 200;
/// Longer than any pairing code, so a typo with extra characters still gets the pairing message.
pub const MAX_PAIRING_CODE_BYTES: usize = 32;

/// DNS answers live for a minute; the phone forgets them when it leaves the setup network.
const DNS_ANSWER_TTL_S: u32 = 60;
const DNS_HEADER_BYTES: usize = 12;
const DNS_TYPE_A: u16 = 1;
const DNS_TYPE_ANY: u16 = 255;
const DNS_CLASS_IN: u16 = 1;
const DNS_MAX_MESSAGE_BYTES: usize = 512;

/// The form fields as typed, after URL decoding. Re-shown when a check fails.
#[derive(Clone, Debug, Default, PartialEq, Eq)]
pub struct SetupFormValues {
    pub wifi_ssid: String,
    pub wifi_pass: String,
    pub server_url: String,
    pub pairing_code: String,
}

#[derive(Clone, Copy, Debug, PartialEq, Eq)]
pub enum SetupFormError {
    FormTooLong,
    BadEncoding,
    WifiNameMissing,
    WifiNameTooLong,
    WifiPasswordLength,
    ServerUrlTooLong,
    ServerUrlInvalid,
    PairingCodeInvalid,
}

impl SetupFormError {
    /// Plain-language text shown above the form.
    pub fn message(self) -> &'static str {
        match self {
            Self::FormTooLong => "That was too much text. Check each box and try again.",
            Self::BadEncoding => "Some of that text could not be read. Check each box and try again.",
            Self::WifiNameMissing => "Enter the name of your Wi-Fi network.",
            Self::WifiNameTooLong => "That Wi-Fi name is too long. Wi-Fi names are at most 32 characters.",
            Self::WifiPasswordLength => {
                "The Wi-Fi password must be 8 to 64 characters. Leave it empty only if your Wi-Fi has no password."
            }
            Self::ServerUrlTooLong => "That server address is too long.",
            Self::ServerUrlInvalid => {
                "The server address must start with https:// or http://, like https://stockbot.rhionin.com."
            }
            Self::PairingCodeInvalid => {
                "That pairing code is not right. It looks like SBP-XXXX-XXXX-X. Check it, or leave it empty and pair later."
            }
        }
    }
}

/// Decodes an `application/x-www-form-urlencoded` body. Unknown fields are ignored and the
/// last copy of a repeated field wins.
pub fn decode_setup_form(body: &[u8]) -> Result<SetupFormValues, SetupFormError> {
    if body.len() > MAX_SETUP_FORM_BYTES {
        return Err(SetupFormError::FormTooLong);
    }
    let mut values = SetupFormValues {
        server_url: DEFAULT_SERVER_URL.to_owned(),
        ..SetupFormValues::default()
    };
    for pair in body.split(|&b| b == b'&') {
        if pair.is_empty() {
            continue;
        }
        let (name, value) = match pair.iter().position(|&b| b == b'=') {
            Some(eq) => (&pair[..eq], &pair[eq + 1..]),
            None => (pair, &pair[pair.len()..]),
        };
        let name = form_url_decode(name)?;
        let value = form_url_decode(value)?;
        match name.as_str() {
            "ssid" => values.wifi_ssid = value,
            "pass" => values.wifi_pass = value,
            "url" => values.server_url = value,
            "code" => values.pairing_code = value,
            _ => {}
        }
    }
    Ok(values)
}

/// Checks typed values and returns what the controller saves. An empty server address means
/// the default server; an empty pairing code means pair later.
pub fn validate_setup_form(
    values: &SetupFormValues,
) -> Result<CaptivePortalSubmission, SetupFormError> {
    if values.wifi_ssid.trim().is_empty() {
        return Err(SetupFormError::WifiNameMissing);
    }
    if values.wifi_ssid.len() > MAX_WIFI_NAME_BYTES {
        return Err(SetupFormError::WifiNameTooLong);
    }
    let pass_len = values.wifi_pass.len();
    if pass_len != 0 && !(MIN_WIFI_PASSWORD_BYTES..=MAX_WIFI_PASSWORD_BYTES).contains(&pass_len) {
        return Err(SetupFormError::WifiPasswordLength);
    }
    let server_url = validate_server_url(&values.server_url)?;
    let pairing_code = validate_pairing_code(&values.pairing_code)?;
    Ok(CaptivePortalSubmission {
        ready: true,
        wifi_ssid: values.wifi_ssid.clone(),
        wifi_pass: values.wifi_pass.clone(),
        server_url,
        pairing_code,
    })
}

pub fn parse_setup_form(body: &[u8]) -> Result<CaptivePortalSubmission, SetupFormError> {
    validate_setup_form(&decode_setup_form(body)?)
}

fn validate_server_url(raw: &str) -> Result<String, SetupFormError> {
    let url = raw.trim();
    if url.is_empty() {
        return Ok(DEFAULT_SERVER_URL.to_owned());
    }
    if url.len() > MAX_SERVER_URL_BYTES {
        return Err(SetupFormError::ServerUrlTooLong);
    }
    if transport_for_url(url).is_none() {
        return Err(SetupFormError::ServerUrlInvalid);
    }
    if url.chars().any(|ch| ch.is_whitespace() || ch.is_control()) {
        return Err(SetupFormError::ServerUrlInvalid);
    }
    let after_scheme = &url[url.find("://").map_or(0, |at| at + 3)..];
    let authority = after_scheme
        .split(['/', '?', '#'])
        .next()
        .unwrap_or_default();
    let host = authority.rsplit('@').next().unwrap_or_default();
    let host = match host.rfind(':') {
        Some(colon) if !host.ends_with(']') => {
            let port = &host[colon + 1..];
            if port.is_empty() || port.parse::<u16>().is_err() {
                return Err(SetupFormError::ServerUrlInvalid);
            }
            &host[..colon]
        }
        _ => host,
    };
    if host.is_empty() {
        return Err(SetupFormError::ServerUrlInvalid);
    }
    Ok(url.trim_end_matches('/').to_owned())
}

fn validate_pairing_code(raw: &str) -> Result<String, SetupFormError> {
    if raw.trim().is_empty() {
        return Ok(String::new());
    }
    if raw.len() > MAX_PAIRING_CODE_BYTES || !is_pairing_code(raw) {
        return Err(SetupFormError::PairingCodeInvalid);
    }
    normalize_scan(raw).ok_or(SetupFormError::PairingCodeInvalid)
}

fn form_url_decode(raw: &[u8]) -> Result<String, SetupFormError> {
    let mut out = Vec::with_capacity(raw.len());
    let mut i = 0;
    while i < raw.len() {
        match raw[i] {
            b'+' => out.push(b' '),
            b'%' => {
                let hi = raw.get(i + 1).and_then(|b| hex_value(*b));
                let lo = raw.get(i + 2).and_then(|b| hex_value(*b));
                let (Some(hi), Some(lo)) = (hi, lo) else {
                    return Err(SetupFormError::BadEncoding);
                };
                out.push((hi << 4) | lo);
                i += 2;
            }
            b => out.push(b),
        }
        i += 1;
    }
    String::from_utf8(out).map_err(|_| SetupFormError::BadEncoding)
}

fn hex_value(b: u8) -> Option<u8> {
    match b {
        b'0'..=b'9' => Some(b - b'0'),
        b'a'..=b'f' => Some(b - b'a' + 10),
        b'A'..=b'F' => Some(b - b'A' + 10),
        _ => None,
    }
}

/// Escapes text for an HTML body or a double-quoted attribute.
pub fn html_escape(text: &str) -> String {
    let mut out = String::with_capacity(text.len());
    for ch in text.chars() {
        match ch {
            '&' => out.push_str("&amp;"),
            '<' => out.push_str("&lt;"),
            '>' => out.push_str("&gt;"),
            '"' => out.push_str("&quot;"),
            '\'' => out.push_str("&#39;"),
            _ => out.push(ch),
        }
    }
    out
}

const PAGE_HEAD: &str = "<!doctype html><html lang=\"en\"><head><meta charset=\"utf-8\">\
<meta name=\"viewport\" content=\"width=device-width,initial-scale=1\">\
<title>Set up your scanner</title><style>\
body{font-family:system-ui,sans-serif;margin:0;padding:16px;background:#f8fafc;color:#0f172a}\
main{max-width:420px;margin:0 auto}h1{font-size:1.4em}\
label{display:block;margin-top:14px;font-weight:600}\
small{display:block;font-weight:400;color:#475569}\
input{box-sizing:border-box;width:100%;padding:10px;margin-top:4px;font-size:1em;\
border:1px solid #94a3b8;border-radius:6px}\
button{margin-top:20px;width:100%;padding:12px;font-size:1.1em;border:0;border-radius:6px;\
background:#2563eb;color:#fff}\
.error{background:#fee2e2;color:#991b1b;padding:10px;border-radius:6px}\
</style></head><body><main>";
const PAGE_TAIL: &str = "</main></body></html>";

/// The setup form. The password box always starts empty so it is never echoed back.
pub fn render_setup_page(values: &SetupFormValues, error: Option<SetupFormError>) -> String {
    let mut page = String::with_capacity(3072);
    page.push_str(PAGE_HEAD);
    page.push_str("<h1>Set up your scanner</h1>");
    page.push_str("<p>Connect the scanner to your Wi-Fi.</p>");
    if let Some(err) = error {
        page.push_str("<p class=\"error\" role=\"alert\">");
        page.push_str(&html_escape(err.message()));
        page.push_str("</p>");
    }
    let server_url = if values.server_url.is_empty() {
        DEFAULT_SERVER_URL
    } else {
        values.server_url.as_str()
    };
    page.push_str("<form method=\"post\" action=\"/\">");
    page.push_str(
        "<label for=\"ssid\">Wi-Fi name</label>\
<input id=\"ssid\" name=\"ssid\" required maxlength=\"32\" autocapitalize=\"none\" \
autocorrect=\"off\" spellcheck=\"false\" value=\"",
    );
    page.push_str(&html_escape(&values.wifi_ssid));
    page.push_str("\">");
    page.push_str(
        "<label for=\"pass\">Wi-Fi password<small>Leave empty if your Wi-Fi has no password.</small></label>\
<input id=\"pass\" name=\"pass\" type=\"password\" maxlength=\"64\" autocomplete=\"off\">",
    );
    page.push_str(
        "<label for=\"url\">Server address<small>Keep this unless you run your own Stockbot server.</small></label>\
<input id=\"url\" name=\"url\" type=\"url\" maxlength=\"200\" autocapitalize=\"none\" \
autocorrect=\"off\" spellcheck=\"false\" value=\"",
    );
    page.push_str(&html_escape(server_url));
    page.push_str("\">");
    page.push_str(
        "<label for=\"code\">Pairing code (optional)<small>From Stockbot on your computer, like SBP-XXXX-XXXX-X. You can also scan it later.</small></label>\
<input id=\"code\" name=\"code\" maxlength=\"32\" autocapitalize=\"characters\" \
autocorrect=\"off\" spellcheck=\"false\" value=\"",
    );
    page.push_str(&html_escape(&values.pairing_code));
    page.push_str("\">");
    page.push_str("<button type=\"submit\">Save and connect</button></form>");
    page.push_str(PAGE_TAIL);
    page
}

/// Shown after a good submission, just before the setup network goes away.
pub fn render_setup_saved_page(wifi_ssid: &str) -> String {
    let mut page = String::with_capacity(1536);
    page.push_str(PAGE_HEAD);
    page.push_str("<h1>Saved</h1><p>The scanner is joining <b>");
    page.push_str(&html_escape(wifi_ssid));
    page.push_str(
        "</b> now. This setup network will go away, and your phone will reconnect to your usual Wi-Fi.</p>\
<p>Check the scanner screen to see how it went.</p>",
    );
    page.push_str(PAGE_TAIL);
    page
}

/// Answers a DNS query with `answer_ip` (the portal passes [`PORTAL_IPV4`]) for A and ANY
/// questions, and with an empty success for every other type, so phones fall back to IPv4.
/// Returns `None` for anything that is not a single-question standard query.
pub fn build_dns_answer(query: &[u8], answer_ip: [u8; 4]) -> Option<Vec<u8>> {
    if query.len() < DNS_HEADER_BYTES || query.len() > DNS_MAX_MESSAGE_BYTES {
        return None;
    }
    let flags = u16::from_be_bytes([query[2], query[3]]);
    let is_response = flags & 0x8000 != 0;
    let opcode = (flags >> 11) & 0x0F;
    let question_count = u16::from_be_bytes([query[4], query[5]]);
    if is_response || opcode != 0 || question_count != 1 {
        return None;
    }

    let mut at = DNS_HEADER_BYTES;
    loop {
        let len = usize::from(*query.get(at)?);
        if len == 0 {
            at += 1;
            break;
        }
        if len & 0xC0 != 0 {
            return None;
        }
        at += 1 + len;
    }
    let question_end = at + 4;
    let qtype = u16::from_be_bytes([*query.get(at)?, *query.get(at + 1)?]);
    let qclass = u16::from_be_bytes([*query.get(at + 2)?, *query.get(at + 3)?]);

    let answers = (qtype == DNS_TYPE_A || qtype == DNS_TYPE_ANY) && qclass == DNS_CLASS_IN;
    let recursion_desired = flags & 0x0100;
    let response_flags: u16 = 0x8000 | 0x0400 | recursion_desired;

    let mut out = Vec::with_capacity(question_end + 16);
    out.extend_from_slice(&query[0..2]);
    out.extend_from_slice(&response_flags.to_be_bytes());
    out.extend_from_slice(&1u16.to_be_bytes());
    out.extend_from_slice(&u16::from(answers).to_be_bytes());
    out.extend_from_slice(&0u16.to_be_bytes());
    out.extend_from_slice(&0u16.to_be_bytes());
    out.extend_from_slice(&query[DNS_HEADER_BYTES..question_end]);
    if answers {
        out.extend_from_slice(&[0xC0, 0x0C]);
        out.extend_from_slice(&DNS_TYPE_A.to_be_bytes());
        out.extend_from_slice(&DNS_CLASS_IN.to_be_bytes());
        out.extend_from_slice(&DNS_ANSWER_TTL_S.to_be_bytes());
        out.extend_from_slice(&4u16.to_be_bytes());
        out.extend_from_slice(&answer_ip);
    }
    Some(out)
}
