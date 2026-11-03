package scanlistener

import (
	"bytes"
	"encoding/binary"
	"io"
	"testing"
)

func TestIsKeyPress(t *testing.T) {
	tests := []struct {
		name     string
		event    KeyEvent
		expected bool
	}{
		{"EV_KEY, press", KeyEvent{Type: evKey, Code: 0x02, Value: valuePress}, true},
		{"EV_KEY, release", KeyEvent{Type: evKey, Code: 0x02, Value: 0}, false},
		{"EV_KEY, auto-repeat", KeyEvent{Type: evKey, Code: 0x02, Value: 2}, false},
		{"EV_SYN, press", KeyEvent{Type: 0x00, Code: 0x00, Value: valuePress}, false},
		{"EV_ABS, press", KeyEvent{Type: 0x03, Code: 0x00, Value: valuePress}, false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := tt.event.IsKeyPress(); got != tt.expected {
				t.Errorf("IsKeyPress() = %v, want %v", got, tt.expected)
			}
		})
	}
}

func TestReadKeyEvent(t *testing.T) {
	// Build a valid input_event record manually
	var buf bytes.Buffer
	buf.Write(make([]byte, 16))                            // padding for timeval (unused)
	binary.Write(&buf, binary.NativeEndian, uint16(evKey)) // type
	binary.Write(&buf, binary.NativeEndian, uint16(0x02))  // code
	binary.Write(&buf, binary.NativeEndian, int32(1))      // value

	// Test successful decode
	reader := bytes.NewReader(buf.Bytes())
	k, err := readKeyEvent(reader)
	if err != nil {
		t.Fatalf("readKeyEvent failed: %v", err)
	}
	if k.Type != evKey || k.Code != 0x02 || k.Value != 1 {
		t.Errorf("readKeyEvent = %+v, want Type=%d Code=0x02 Value=1", k, evKey)
	}

	// Test short read
	short := bytes.NewBuffer(buf.Bytes()[:12])
	_, err = readKeyEvent(short)
	if err != io.ErrUnexpectedEOF {
		t.Errorf("short read error = %v, want %v", err, io.ErrUnexpectedEOF)
	}

	// Test empty read
	empty := bytes.NewReader(nil)
	_, err = readKeyEvent(empty)
	if err != io.EOF {
		t.Errorf("empty read error = %v, want %v", err, io.EOF)
	}
}

// Test readKeyEvent with two concatenated records
func TestReadKeyEventMultiple(t *testing.T) {
	var buf bytes.Buffer
	// First record: EV_KEY, code 0x02, value 1
	buf.Write(make([]byte, 16))
	binary.Write(&buf, binary.NativeEndian, uint16(evKey))
	binary.Write(&buf, binary.NativeEndian, uint16(0x02))
	binary.Write(&buf, binary.NativeEndian, int32(1))

	// Second record: EV_KEY, code 0x03, value 1
	buf.Write(make([]byte, 16))
	binary.Write(&buf, binary.NativeEndian, uint16(evKey))
	binary.Write(&buf, binary.NativeEndian, uint16(0x03))
	binary.Write(&buf, binary.NativeEndian, int32(1))

	reader := bytes.NewReader(buf.Bytes())

	k1, err := readKeyEvent(reader)
	if err != nil || k1.Type != evKey || k1.Code != 0x02 || k1.Value != 1 {
		t.Fatalf("first record failed: %+v, %v", k1, err)
	}

	k2, err := readKeyEvent(reader)
	if err != nil || k2.Type != evKey || k2.Code != 0x03 || k2.Value != 1 {
		t.Fatalf("second record failed: %+v, %v", k2, err)
	}
}
