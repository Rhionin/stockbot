//! USB HID keyboard report → barcode line decoder.
pub const HID_KEY_ENTER: u8 = 0x28;
pub const HID_KEY_BACKSPACE: u8 = 0x2A;

pub struct HidKeycodeDecoder {
    line: String,
    last_keys: [u8; 6],
}

impl Default for HidKeycodeDecoder {
    fn default() -> Self {
        Self::new()
    }
}

impl HidKeycodeDecoder {
    pub fn new() -> Self {
        Self {
            line: String::new(),
            last_keys: [0; 6],
        }
    }

    pub fn reset_line(&mut self) {
        self.line.clear();
    }

    pub fn line_buffer(&self) -> &str {
        &self.line
    }

    pub fn consume_report(&mut self, report: &[u8; 8]) -> Option<String> {
        let modifier = report[0];
        let shift = (modifier & 0x22) != 0;
        let mut saw_enter = false;
        let mut current = [0u8; 6];

        for i in 0..6 {
            let key = report[2 + i];
            current[i] = key;
            if key == 0 || key_was_down(key, &self.last_keys) {
                continue;
            }
            if key == HID_KEY_ENTER {
                saw_enter = true;
                continue;
            }
            if key == HID_KEY_BACKSPACE && !self.line.is_empty() {
                self.line.pop();
                continue;
            }
            let ch = hid_usage_to_ascii(key, shift);
            if ch != '\0' {
                self.line.push(ch);
            }
        }
        self.last_keys = current;
        if saw_enter && !self.line.is_empty() {
            let completed = std::mem::take(&mut self.line);
            return Some(completed);
        }
        None
    }
}

fn key_was_down(key: u8, previous: &[u8; 6]) -> bool {
    if key == 0 {
        return false;
    }
    previous.contains(&key)
}

fn hid_usage_to_ascii(usage: u8, shift: bool) -> char {
    const USAGE_A: u8 = 0x04;
    const USAGE_Z: u8 = 0x1D;
    const USAGE_1: u8 = 0x1E;
    const USAGE_9: u8 = 0x26;
    const USAGE_0: u8 = 0x27;
    const USAGE_MINUS: u8 = 0x2D;
    const USAGE_EQUAL: u8 = 0x2E;
    const USAGE_L_BRACKET: u8 = 0x2F;
    const USAGE_R_BRACKET: u8 = 0x30;
    const USAGE_BACKSLASH: u8 = 0x31;

    if (USAGE_A..=USAGE_Z).contains(&usage) {
        let base = char::from(b'a' + (usage - USAGE_A));
        if shift {
            char::from(base as u8 - b'a' + b'A')
        } else {
            base
        }
    } else if (USAGE_1..=USAGE_9).contains(&usage) {
        char::from(b'1' + (usage - USAGE_1))
    } else if usage == USAGE_0 {
        '0'
    } else if usage == USAGE_MINUS {
        if shift {
            '_'
        } else {
            '-'
        }
    } else if usage == USAGE_EQUAL {
        if shift {
            '+'
        } else {
            '='
        }
    } else if usage == USAGE_BACKSLASH {
        if shift {
            '|'
        } else {
            '\\'
        }
    } else if usage == USAGE_L_BRACKET {
        if shift {
            '{'
        } else {
            '['
        }
    } else if usage == USAGE_R_BRACKET {
        if shift {
            '}'
        } else {
            ']'
        }
    } else {
        '\0'
    }
}
