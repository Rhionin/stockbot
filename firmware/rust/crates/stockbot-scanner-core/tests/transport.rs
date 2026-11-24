use stockbot_scanner_core::{transport_for_url, Transport};

#[test]
fn https_urls_use_verified_tls() {
    for url in [
        "https://stockbot.rhionin.com",
        "https://10.0.0.5",
        "HTTPS://x",
        "HtTpS://mixed.example",
    ] {
        assert_eq!(
            transport_for_url(url),
            Some(Transport::VerifiedTls),
            "{url}"
        );
    }
}

#[test]
fn http_urls_use_plain_http() {
    for url in ["http://192.168.1.10:8080", "HTTP://pi:8080", "http://pi"] {
        assert_eq!(transport_for_url(url), Some(Transport::PlainHttp), "{url}");
    }
}

#[test]
fn other_urls_have_no_transport() {
    for url in [
        "ftp://x",
        "",
        "stockbot.rhionin.com",
        "https:/x",
        "httpx://x",
        " https://x",
        "http",
        "é",
    ] {
        assert_eq!(transport_for_url(url), None, "{url:?}");
    }
}

#[test]
fn each_call_decides_its_own_transport() {
    let seen = [
        transport_for_url("https://stockbot.rhionin.com"),
        transport_for_url("http://192.168.1.10:8080"),
        transport_for_url("https://stockbot.rhionin.com"),
    ];
    assert_eq!(
        seen,
        [
            Some(Transport::VerifiedTls),
            Some(Transport::PlainHttp),
            Some(Transport::VerifiedTls)
        ]
    );
}
