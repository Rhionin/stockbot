//! URL scheme to transport rule for device HTTP calls.

/// How the firmware talks to a server URL.
#[derive(Clone, Copy, Debug, PartialEq, Eq)]
pub enum Transport {
    /// TLS with certificate verification against the bundled roots.
    VerifiedTls,
    /// Plain HTTP, for a LAN server such as `http://pi:8080`.
    PlainHttp,
}

/// `https://` selects [`Transport::VerifiedTls`] and `http://` selects [`Transport::PlainHttp`]
/// (scheme compared ASCII case-insensitively). Any other URL has no transport.
///
/// Each request decides its own mode from its URL; nothing is remembered between calls.
pub fn transport_for_url(url: &str) -> Option<Transport> {
    if starts_with_ignore_ascii_case(url, "https://") {
        Some(Transport::VerifiedTls)
    } else if starts_with_ignore_ascii_case(url, "http://") {
        Some(Transport::PlainHttp)
    } else {
        None
    }
}

fn starts_with_ignore_ascii_case(text: &str, prefix: &str) -> bool {
    text.as_bytes()
        .get(..prefix.len())
        .is_some_and(|head| head.eq_ignore_ascii_case(prefix.as_bytes()))
}
