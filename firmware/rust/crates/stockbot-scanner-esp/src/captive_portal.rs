//! Captive portal (#745): an open `Scanner-Setup-XXXX` network, a DNS answerer that sends every
//! name to the scanner, and the Wi-Fi setup page. Form checks and page text live in the core.

use std::net::{Ipv4Addr, UdpSocket};
use std::sync::{Arc, Mutex, PoisonError};
use std::time::{Duration, Instant};

use esp_idf_svc::http::server::{Configuration, EspHttpConnection, EspHttpServer, Request};
use esp_idf_svc::http::Method;
use esp_idf_svc::io::{EspIOError, Write};
use stockbot_scanner_core::{
    build_dns_answer, decode_setup_form, render_setup_page, render_setup_saved_page,
    scanner_setup_ssid, validate_setup_form, CaptivePortalHal, CaptivePortalSubmission,
    SetupFormValues, MAX_SETUP_FORM_BYTES, PORTAL_IPV4,
};

use crate::wifi::SharedRadio;

const DNS_PORT: u16 = 53;
/// Lets the "Saved" page reach the phone before the setup network goes away.
const SAVED_PAGE_GRACE: Duration = Duration::from_millis(1500);
const HTTP_STACK_BYTES: usize = 8 * 1024;
/// Caps the lookups answered per controller loop so a chatty phone cannot stall scanning.
const DNS_QUERIES_PER_LOOP: usize = 8;
const DNS_BUFFER_BYTES: usize = 512;
const HTML_HEADERS: &[(&str, &str)] = &[
    ("Content-Type", "text/html; charset=utf-8"),
    ("Cache-Control", "no-store"),
];

/// A good submission waiting for the controller, stamped with when the phone got its answer.
type Inbox = Arc<Mutex<Option<(Instant, CaptivePortalSubmission)>>>;

struct PortalSession {
    _server: EspHttpServer<'static>,
    dns: UdpSocket,
}

pub struct EspCaptivePortal {
    radio: SharedRadio,
    inbox: Inbox,
    session: Option<PortalSession>,
}

impl EspCaptivePortal {
    pub fn new(radio: SharedRadio) -> Self {
        Self {
            radio,
            inbox: Arc::new(Mutex::new(None)),
            session: None,
        }
    }

    fn open_session(&self) -> Result<PortalSession, String> {
        let mut server = EspHttpServer::new(&Configuration {
            stack_size: HTTP_STACK_BYTES,
            uri_match_wildcard: true,
            ..Default::default()
        })
        .map_err(|err| format!("the setup page did not start: {err}"))?;
        server
            .fn_handler("/*", Method::Get, serve_form)
            .map_err(|err| format!("the setup page did not start: {err}"))?;
        let inbox = Arc::clone(&self.inbox);
        server
            .fn_handler("/*", Method::Post, move |req| receive_form(req, &inbox))
            .map_err(|err| format!("the setup page did not start: {err}"))?;

        let dns = UdpSocket::bind((Ipv4Addr::UNSPECIFIED, DNS_PORT))
            .and_then(|socket| socket.set_nonblocking(true).map(|()| socket))
            .map_err(|err| format!("the setup DNS did not start: {err}"))?;
        Ok(PortalSession {
            _server: server,
            dns,
        })
    }

    fn answer_dns(dns: &UdpSocket) {
        let mut buf = [0u8; DNS_BUFFER_BYTES];
        for _ in 0..DNS_QUERIES_PER_LOOP {
            let Ok((len, from)) = dns.recv_from(&mut buf) else {
                return;
            };
            if let Some(answer) = build_dns_answer(&buf[..len], PORTAL_IPV4) {
                if let Err(err) = dns.send_to(&answer, from) {
                    log::debug!("portal: DNS answer to {from} failed: {err}");
                }
            }
        }
    }
}

impl CaptivePortalHal for EspCaptivePortal {
    fn start(&mut self, ap_suffix: &str) {
        if self.session.is_some() {
            return;
        }
        *self.inbox.lock().unwrap_or_else(PoisonError::into_inner) = None;
        let ssid = scanner_setup_ssid(ap_suffix);
        if let Err(err) = self.radio.borrow_mut().start_access_point(&ssid) {
            log::error!("portal: the setup network {ssid} did not start: {err}");
            return;
        }
        match self.open_session() {
            Ok(session) => {
                self.session = Some(session);
                log::info!("portal: join {ssid}, then open http://192.168.4.1 if no page appears");
            }
            Err(err) => {
                log::error!("portal: {err}");
                if let Err(err) = self.radio.borrow_mut().stop_access_point() {
                    log::warn!("portal: stopping the setup network failed: {err}");
                }
            }
        }
    }

    fn stop(&mut self) {
        let was_running = self.session.take().is_some();
        *self.inbox.lock().unwrap_or_else(PoisonError::into_inner) = None;
        if let Err(err) = self.radio.borrow_mut().stop_access_point() {
            log::warn!("portal: stopping the setup network failed: {err}");
        }
        if was_running {
            log::info!("portal: setup network closed");
        }
    }

    fn loop_portal(&mut self) {
        if let Some(session) = &self.session {
            Self::answer_dns(&session.dns);
        }
    }

    fn take_submission(&mut self, out: &mut CaptivePortalSubmission) -> bool {
        let mut inbox = self.inbox.lock().unwrap_or_else(PoisonError::into_inner);
        match inbox.as_ref() {
            Some((saved_at, _)) if saved_at.elapsed() >= SAVED_PAGE_GRACE => {}
            _ => return false,
        }
        let Some((_, submission)) = inbox.take() else {
            return false;
        };
        *out = submission;
        true
    }
}

/// Phones probe a known address to spot a captive network; sending every other host to the
/// portal address makes them open the setup page by themselves.
fn serve_form(req: Request<&mut EspHttpConnection>) -> Result<(), EspIOError> {
    let portal_host = Ipv4Addr::from(PORTAL_IPV4).to_string();
    let host = req.header("Host").and_then(|host| host.split(':').next());
    if host.is_some_and(|host| host != portal_host) {
        let location = format!("http://{portal_host}/");
        req.into_response(
            302,
            Some("Found"),
            &[
                ("Location", location.as_str()),
                ("Cache-Control", "no-store"),
            ],
        )?;
        return Ok(());
    }
    let page = render_setup_page(&SetupFormValues::default(), None);
    req.into_response(200, Some("OK"), HTML_HEADERS)?
        .write_all(page.as_bytes())
}

fn receive_form(mut req: Request<&mut EspHttpConnection>, inbox: &Inbox) -> Result<(), EspIOError> {
    let mut body = Vec::new();
    let mut chunk = [0u8; 256];
    while body.len() <= MAX_SETUP_FORM_BYTES {
        let read = req.read(&mut chunk)?;
        if read == 0 {
            break;
        }
        body.extend_from_slice(&chunk[..read]);
    }

    let (values, outcome) = match decode_setup_form(&body) {
        Ok(values) => {
            let outcome = validate_setup_form(&values);
            (values, outcome)
        }
        Err(err) => (SetupFormValues::default(), Err(err)),
    };
    match outcome {
        Ok(submission) => {
            let page = render_setup_saved_page(&submission.wifi_ssid);
            req.into_response(200, Some("OK"), HTML_HEADERS)?
                .write_all(page.as_bytes())?;
            log::info!(
                "portal: settings saved for Wi-Fi {} (pairing code: {})",
                submission.wifi_ssid,
                if submission.pairing_code.is_empty() {
                    "no"
                } else {
                    "yes"
                }
            );
            *inbox.lock().unwrap_or_else(PoisonError::into_inner) =
                Some((Instant::now(), submission));
            Ok(())
        }
        Err(err) => {
            log::info!("portal: form refused: {err:?}");
            let page = render_setup_page(&values, Some(err));
            req.into_response(200, Some("OK"), HTML_HEADERS)?
                .write_all(page.as_bytes())
        }
    }
}
