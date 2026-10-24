package scanlistener

import "testing"

func TestDecodeKey(t *testing.T) {
	tests := []struct {
		name     string
		code     uint16
		shift    bool
		wantRune rune
		wantKind keyKind
	}{
		// Digit row
		{"KEY_1 unshifted", 0x02, false, '1', keyPrintable},
		{"KEY_1 shifted", 0x02, true, '!', keyPrintable},
		{"KEY_2 unshifted", 0x03, false, '2', keyPrintable},
		{"KEY_3 unshifted", 0x04, false, '3', keyPrintable},
		{"KEY_4 unshifted", 0x05, false, '4', keyPrintable},
		{"KEY_5 unshifted", 0x06, false, '5', keyPrintable},
		{"KEY_6 unshifted", 0x07, false, '6', keyPrintable},
		{"KEY_7 unshifted", 0x08, false, '7', keyPrintable},
		{"KEY_8 unshifted", 0x09, false, '8', keyPrintable},
		{"KEY_9 unshifted", 0x0a, false, '9', keyPrintable},
		{"KEY_0 unshifted", 0x0b, false, '0', keyPrintable},
		{"KEY_MINUS unshifted", 0x0c, false, '-', keyPrintable},
		{"KEY_MINUS shifted", 0x0c, true, '_', keyPrintable},
		{"KEY_EQUAL unshifted", 0x0d, false, '=', keyPrintable},
		{"KEY_EQUAL shifted", 0x0d, true, '+', keyPrintable},

		// Letter row (uppercase with shift)
		{"KEY_A unshifted", 0x1e, false, 'a', keyPrintable},
		{"KEY_A shifted", 0x1e, true, 'A', keyPrintable},
		{"KEY_Q unshifted", 0x10, false, 'q', keyPrintable},
		{"KEY_Q shifted", 0x10, true, 'Q', keyPrintable},
		{"KEY_Z unshifted", 0x2c, false, 'z', keyPrintable},
		{"KEY_Z shifted", 0x2c, true, 'Z', keyPrintable},

		// Middle row
		{"KEY_SEMICOLON unshifted", 0x27, false, ';', keyPrintable},
		{"KEY_SEMICOLON shifted", 0x27, true, ':', keyPrintable},

		// Lower row
		{"KEY_COMMA unshifted", 0x33, false, ',', keyPrintable},
		{"KEY_COMMA shifted", 0x33, true, '<', keyPrintable},
		{"KEY_SLASH unshifted", 0x35, false, '/', keyPrintable},
		{"KEY_SLASH shifted", 0x35, true, '?', keyPrintable},

		// Keypad digits
		{"KEY_KP0", 0x52, false, '0', keyPrintable},
		{"KEY_KP1", 0x53, false, '1', keyPrintable},
		{"KEY_KP9", 0x5b, false, '9', keyPrintable},

		// Enter keys
		{"KEY_ENTER (0x1c)", 0x1c, false, 0, keyEnter},
		{"KEY_KPENTER (0x60, numpad)", 0x60, false, 0, keyEnter},

		// Shift keys
		{"KEY_LEFTSHIFT (0x2a)", 0x2a, false, 0, keyShift},
		{"KEY_RIGHTSHIFT (0x36)", 0x36, false, 0, keyShift},

		// Space
		{"KEY_SPACE", 0x39, false, ' ', keyPrintable},

		// Unmapped
		{"unknown high keycode", 0xff, false, 0, keyUnmapped},
		{"unknown low keycode", 0x40, false, 0, keyUnmapped},
	}

	for code := uint16(0); code < 0x70; code++ {
		decodeKey(code, false)
		decodeKey(code, true)
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			rune, kind := decodeKey(tt.code, tt.shift)
			if rune != tt.wantRune {
				t.Errorf("decodeKey(%d, %v) rune = %q, want %q", tt.code, tt.shift, rune, tt.wantRune)
			}
			if kind != tt.wantKind {
				t.Errorf("decodeKey(%d, %v) kind = %v, want %v", tt.code, tt.shift, kind, tt.wantKind)
			}
		})
	}
}
