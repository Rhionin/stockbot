//! USB host driver for one boot-protocol keyboard on the ESP32-S3 OTG port: the Symcode
//! MJ-2030-B 2.4 GHz dongle on the AtomS3 Lite (#748).
//!
//! `esp-idf-svc` has no safe USB host wrapper, so this crate calls ESP-IDF's `usb_host` library
//! directly. It is the only firmware crate allowed to use `unsafe` (CJ's ruling on #445,
//! 2026-11-26). Every `unsafe` block carries a `// SAFETY:` comment, and the public API is safe:
//! [`UsbKeyboard::start`] and [`UsbKeyboard::next_event`].
//!
//! Two threads own all USB host state. The library thread pumps the host library's events. The
//! client thread registers one client and pumps its events; the host library runs the client and
//! transfer callbacks on that thread, inside `usb_host_client_handle_events`, so the client state
//! is only ever touched from one thread and never while another borrow of it is live. Reports
//! leave through a channel.

#![deny(unsafe_op_in_unsafe_fn)]
#![deny(clippy::undocumented_unsafe_blocks)]

use std::ffi::c_void;
use std::fmt;
use std::ptr;
use std::sync::atomic::{AtomicBool, Ordering};
use std::sync::mpsc::{self, Receiver, Sender, SyncSender, TryRecvError};

use esp_idf_svc::hal::delay::{TickType, BLOCK};
use esp_idf_svc::sys::{self, EspError};
use stockbot_scanner_core::usb_hid::{
    boot_report_from, find_interrupt_in_endpoint, InterruptInEndpoint, KEYBOARD_INTERFACE,
};

const LIBRARY_THREAD_STACK_BYTES: usize = 4 * 1024;
const CLIENT_THREAD_STACK_BYTES: usize = 6 * 1024;
const CLIENT_EVENT_QUEUE: i32 = 5;
/// The client thread also wakes this often to retry a keyboard transfer that failed to resubmit.
const CLIENT_WAKE_MS: u64 = 100;
const CONFIG_DESCRIPTOR_HEADER_LEN: usize = 9;
/// `ESP_INTR_FLAG_LEVEL1`; the macro is not in the generated bindings.
const INTR_FLAG_LEVEL1: i32 = 1 << 1;

static STARTED: AtomicBool = AtomicBool::new(false);

#[derive(Debug, Clone, Copy, PartialEq, Eq)]
pub enum KeyboardEvent {
    Connected,
    Report([u8; 8]),
    Removed,
    Rejected(RejectReason),
}

#[derive(Debug, Clone, Copy, PartialEq, Eq)]
pub enum RejectReason {
    OpenFailed,
    NoKeyboardEndpoint,
    ClaimFailed,
    TransferFailed,
    SecondDevice,
}

impl fmt::Display for RejectReason {
    fn fmt(&self, f: &mut fmt::Formatter<'_>) -> fmt::Result {
        f.write_str(match self {
            Self::OpenFailed => "the USB device could not be opened",
            Self::NoKeyboardEndpoint => "the USB device is not a keyboard",
            Self::ClaimFailed => "the USB keyboard is busy",
            Self::TransferFailed => "the USB keyboard stopped answering",
            Self::SecondDevice => "only one USB scanner is supported at a time",
        })
    }
}

#[derive(Debug)]
pub enum StartError {
    AlreadyStarted,
    Install(EspError),
    Register(EspError),
    Thread(std::io::Error),
}

impl fmt::Display for StartError {
    fn fmt(&self, f: &mut fmt::Formatter<'_>) -> fmt::Result {
        match self {
            Self::AlreadyStarted => f.write_str("the USB port is already in use"),
            Self::Install(err) => write!(f, "the USB port could not switch to host mode ({err})"),
            Self::Register(err) => write!(f, "the USB port could not start listening ({err})"),
            Self::Thread(err) => write!(f, "the USB task could not start ({err})"),
        }
    }
}

impl std::error::Error for StartError {}

/// The keyboard events from the OTG port. Only one can exist per boot.
pub struct UsbKeyboard {
    events: Receiver<KeyboardEvent>,
}

impl UsbKeyboard {
    /// Puts the USB port in host mode and starts listening for a keyboard. The port stops being a
    /// serial console from here on.
    pub fn start() -> Result<Self, StartError> {
        if STARTED.swap(true, Ordering::SeqCst) {
            return Err(StartError::AlreadyStarted);
        }
        let config = sys::usb_host_config_t {
            skip_phy_setup: false,
            intr_flags: INTR_FLAG_LEVEL1,
            ..Default::default()
        };
        // SAFETY: `config` is a valid, fully initialised config that outlives the call, and
        // `STARTED` makes this the only install in this boot.
        EspError::convert(unsafe { sys::usb_host_install(&config) })
            .map_err(StartError::Install)?;
        std::thread::Builder::new()
            .name("usb-host".to_owned())
            .stack_size(LIBRARY_THREAD_STACK_BYTES)
            .spawn(run_library)
            .map_err(StartError::Thread)?;

        let (events_tx, events) = mpsc::channel();
        let (ready_tx, ready) = mpsc::sync_channel(1);
        std::thread::Builder::new()
            .name("usb-keyboard".to_owned())
            .stack_size(CLIENT_THREAD_STACK_BYTES)
            .spawn(move || run_client(events_tx, ready_tx))
            .map_err(StartError::Thread)?;
        match ready.recv() {
            Ok(Ok(())) => Ok(Self { events }),
            Ok(Err(err)) => Err(StartError::Register(err)),
            Err(_) => Err(StartError::Thread(std::io::Error::other(
                "the USB task stopped during start",
            ))),
        }
    }

    pub fn next_event(&self) -> Option<KeyboardEvent> {
        match self.events.try_recv() {
            Ok(event) => Some(event),
            Err(TryRecvError::Empty | TryRecvError::Disconnected) => None,
        }
    }
}

fn run_library() {
    loop {
        let mut flags = 0u32;
        // SAFETY: the host library was installed before this thread was spawned and is never
        // uninstalled; `flags` is a valid place for the library to write to.
        let result = unsafe { sys::usb_host_lib_handle_events(BLOCK, &mut flags) };
        if let Err(err) = EspError::convert(result) {
            log::warn!("usb: host events failed: {err}");
        }
    }
}

struct Device {
    handle: sys::usb_device_handle_t,
    transfer: *mut sys::usb_transfer_t,
    claimed: bool,
    in_flight: bool,
    gone: bool,
}

struct Client {
    handle: sys::usb_host_client_handle_t,
    /// This `Client`'s own address, handed to the host library as callback context.
    context: *mut c_void,
    new_devices: Vec<u8>,
    device: Option<Device>,
    events: Sender<KeyboardEvent>,
}

fn run_client(events: Sender<KeyboardEvent>, ready: SyncSender<Result<(), EspError>>) {
    // Leaked on purpose: the host library keeps this address for callbacks for the rest of the
    // boot, and this thread never returns once registered.
    let client: *mut Client = Box::into_raw(Box::new(Client {
        handle: ptr::null_mut(),
        context: ptr::null_mut(),
        new_devices: Vec::new(),
        device: None,
        events,
    }));
    let mut config = sys::usb_host_client_config_t {
        is_synchronous: false,
        max_num_event_msg: CLIENT_EVENT_QUEUE,
        ..Default::default()
    };
    config.__bindgen_anon_1.async_.client_event_callback = Some(on_client_event);
    config.__bindgen_anon_1.async_.callback_arg = client.cast();
    let mut handle: sys::usb_host_client_handle_t = ptr::null_mut();
    // SAFETY: `config` and `handle` are valid for the call. The callback argument is the leaked
    // `Client`, which is never freed.
    let registered = unsafe { sys::usb_host_client_register(&config, &mut handle) };
    if let Err(err) = EspError::convert(registered) {
        let _ = ready.send(Err(err));
        return;
    }
    {
        // SAFETY: `client` came from `Box::into_raw` above and no callback can run before the
        // first `usb_host_client_handle_events` call, so this is the only borrow.
        let state = unsafe { &mut *client };
        state.handle = handle;
        state.context = client.cast();
    }
    let _ = ready.send(Ok(()));

    let wake = TickType::new_millis(CLIENT_WAKE_MS).ticks();
    loop {
        // SAFETY: `handle` is a registered client. The callbacks this runs borrow the `Client`
        // only for their own duration, and no borrow of it is held across this call.
        let result = unsafe { sys::usb_host_client_handle_events(handle, wake) };
        if let Err(err) = EspError::convert(result) {
            if err.code() != sys::ESP_ERR_TIMEOUT as sys::esp_err_t {
                log::warn!("usb: keyboard events failed: {err}");
            }
        }
        // SAFETY: no callback is running now (they run only inside the call above, on this
        // thread), so this is the only borrow of the leaked `Client`.
        let state = unsafe { &mut *client };
        state.service();
    }
}

impl Client {
    fn send(&self, event: KeyboardEvent) {
        let _ = self.events.send(event);
    }

    fn service(&mut self) {
        if let Some(device) = &self.device {
            if device.gone && !device.in_flight {
                self.close_device();
                self.send(KeyboardEvent::Removed);
            } else if !device.gone && !device.in_flight {
                let transfer = device.transfer;
                if submit(transfer) {
                    self.mark_in_flight();
                } else {
                    self.close_device();
                    self.send(KeyboardEvent::Rejected(RejectReason::TransferFailed));
                }
            }
        }
        for address in std::mem::take(&mut self.new_devices) {
            if self.device.is_some() {
                self.send(KeyboardEvent::Rejected(RejectReason::SecondDevice));
                continue;
            }
            match self.open(address) {
                Ok(()) => self.send(KeyboardEvent::Connected),
                Err(reason) => {
                    self.close_device();
                    self.send(KeyboardEvent::Rejected(reason));
                }
            }
        }
    }

    fn mark_in_flight(&mut self) {
        if let Some(device) = self.device.as_mut() {
            device.in_flight = true;
        }
    }

    /// Opens the device, claims its keyboard interface and starts the first transfer. On error the
    /// half-open device is left in `self.device` for [`Self::close_device`].
    fn open(&mut self, address: u8) -> Result<(), RejectReason> {
        let mut handle: sys::usb_device_handle_t = ptr::null_mut();
        // SAFETY: `self.handle` is a registered client and `handle` is a valid out pointer.
        let opened = unsafe { sys::usb_host_device_open(self.handle, address, &mut handle) };
        EspError::convert(opened).map_err(|_| RejectReason::OpenFailed)?;
        let device = self.device.insert(Device {
            handle,
            transfer: ptr::null_mut(),
            claimed: false,
            in_flight: false,
            gone: false,
        });

        let endpoint = keyboard_endpoint(handle).ok_or(RejectReason::NoKeyboardEndpoint)?;
        // SAFETY: the device is open on this client, and interface 0 alternate 0 is the one the
        // descriptor walk found the endpoint in.
        let claimed =
            unsafe { sys::usb_host_interface_claim(self.handle, handle, KEYBOARD_INTERFACE, 0) };
        EspError::convert(claimed).map_err(|_| RejectReason::ClaimFailed)?;
        device.claimed = true;

        let mut transfer: *mut sys::usb_transfer_t = ptr::null_mut();
        // SAFETY: `transfer` is a valid out pointer; the size is 8 to 64 bytes and the transfer
        // is not isochronous.
        let allocated = unsafe {
            sys::usb_host_transfer_alloc(usize::from(endpoint.packet_size), 0, &mut transfer)
        };
        if EspError::convert(allocated).is_err() || transfer.is_null() {
            return Err(RejectReason::TransferFailed);
        }
        device.transfer = transfer;
        prepare_transfer(transfer, handle, endpoint, self.context);
        if !submit(transfer) {
            return Err(RejectReason::TransferFailed);
        }
        device.in_flight = true;
        Ok(())
    }

    /// Frees whatever [`Self::open`] got to. Only called when no transfer is in flight.
    fn close_device(&mut self) {
        let Some(device) = self.device.take() else {
            return;
        };
        if !device.transfer.is_null() {
            // SAFETY: the transfer was allocated by `usb_host_transfer_alloc`, is not in flight,
            // and is dropped from `Device` here so it is never used again.
            unsafe { sys::usb_host_transfer_free(device.transfer) };
        }
        if device.claimed {
            // SAFETY: this client claimed the interface on this open device and no transfer on it
            // is in flight.
            unsafe {
                sys::usb_host_interface_release(self.handle, device.handle, KEYBOARD_INTERFACE)
            };
        }
        // SAFETY: the device was opened by this client and is dropped here, so the handle is
        // never used again.
        unsafe { sys::usb_host_device_close(self.handle, device.handle) };
    }
}

/// The keyboard's interrupt IN endpoint, read from the device's active configuration descriptor.
fn keyboard_endpoint(device: sys::usb_device_handle_t) -> Option<InterruptInEndpoint> {
    let mut desc: *const sys::usb_config_desc_t = ptr::null();
    // SAFETY: `device` is open and `desc` is a valid out pointer.
    let got = unsafe { sys::usb_host_get_active_config_descriptor(device, &mut desc) };
    if EspError::convert(got).is_err() || desc.is_null() {
        return None;
    }
    let base = desc.cast::<u8>();
    // SAFETY: the library returned a configuration descriptor, which starts with the 9-byte
    // header, and keeps it for as long as the device stays open (it is only read here).
    let header = unsafe { std::slice::from_raw_parts(base, CONFIG_DESCRIPTOR_HEADER_LEN) };
    let total = usize::from(u16::from_le_bytes([header[2], header[3]]));
    if total < CONFIG_DESCRIPTOR_HEADER_LEN {
        return None;
    }
    // SAFETY: the library stores the whole configuration, `wTotalLength` bytes, contiguously
    // from the header.
    let full = unsafe { std::slice::from_raw_parts(base, total) };
    find_interrupt_in_endpoint(full, KEYBOARD_INTERFACE)
}

fn prepare_transfer(
    transfer: *mut sys::usb_transfer_t,
    device: sys::usb_device_handle_t,
    endpoint: InterruptInEndpoint,
    context: *mut c_void,
) {
    // SAFETY: the transfer was just allocated, is not in flight, and nothing else refers to it.
    let t = unsafe { &mut *transfer };
    t.device_handle = device;
    t.bEndpointAddress = endpoint.address;
    t.callback = Some(on_transfer_done);
    t.context = context;
    t.num_bytes = i32::from(endpoint.packet_size);
}

fn submit(transfer: *mut sys::usb_transfer_t) -> bool {
    // SAFETY: the transfer was allocated and filled in by `Client::open`, is not in flight, and
    // stays allocated until `Client::close_device`, which only runs when it is not in flight.
    let submitted = unsafe { sys::usb_host_transfer_submit(transfer) };
    EspError::convert(submitted).is_ok()
}

unsafe extern "C" fn on_client_event(
    msg: *const sys::usb_host_client_event_msg_t,
    arg: *mut c_void,
) {
    if msg.is_null() || arg.is_null() {
        return;
    }
    // SAFETY: `arg` is the leaked `Client` given at registration. This runs on the client
    // thread inside `usb_host_client_handle_events`, where nothing else borrows it.
    let client = unsafe { &mut *arg.cast::<Client>() };
    // SAFETY: the library passes a valid message that lives for the duration of the callback.
    let msg = unsafe { &*msg };
    if msg.event == sys::usb_host_client_event_t_USB_HOST_CLIENT_EVENT_NEW_DEV {
        // SAFETY: `new_dev` is the union member the library fills for NEW_DEV events.
        let address = unsafe { msg.__bindgen_anon_1.new_dev.address };
        client.new_devices.push(address);
    } else if msg.event == sys::usb_host_client_event_t_USB_HOST_CLIENT_EVENT_DEV_GONE {
        // SAFETY: `dev_gone` is the union member the library fills for DEV_GONE events.
        let gone = unsafe { msg.__bindgen_anon_1.dev_gone.dev_hdl };
        if let Some(device) = client.device.as_mut().filter(|d| d.handle == gone) {
            device.gone = true;
        }
    }
}

unsafe extern "C" fn on_transfer_done(transfer: *mut sys::usb_transfer_t) {
    if transfer.is_null() {
        return;
    }
    // SAFETY: the library hands back our own transfer, which stays allocated until
    // `Client::close_device` and is not in flight while this callback runs.
    let t = unsafe { &mut *transfer };
    if t.context.is_null() {
        return;
    }
    // SAFETY: `context` is the leaked `Client` set in `prepare_transfer`. Transfer callbacks run
    // on the client thread inside `usb_host_client_handle_events`, where nothing else borrows it.
    let client = unsafe { &mut *t.context.cast::<Client>() };
    let Some(device) = client.device.as_mut() else {
        return;
    };
    device.in_flight = false;
    let status = t.status;
    if status == sys::usb_transfer_status_t_USB_TRANSFER_STATUS_NO_DEVICE {
        // The DEV_GONE event may come later; the device can be closed now either way.
        device.gone = true;
    }
    if status == sys::usb_transfer_status_t_USB_TRANSFER_STATUS_COMPLETED {
        let len = usize::try_from(t.actual_num_bytes)
            .unwrap_or(0)
            .min(t.data_buffer_size);
        // SAFETY: `data_buffer` holds `data_buffer_size` bytes and `len` is at most that.
        let data = unsafe { std::slice::from_raw_parts(t.data_buffer, len) };
        if let Some(report) = boot_report_from(data) {
            let _ = client.events.send(KeyboardEvent::Report(report));
        }
    }
    let stopped = device.gone || status == sys::usb_transfer_status_t_USB_TRANSFER_STATUS_CANCELED;
    if !stopped && submit(transfer) {
        device.in_flight = true;
    }
}
