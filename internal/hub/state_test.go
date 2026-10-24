package hub

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestStateFileIsPrivate(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "nested", "state.json")
	state := State{
		ServerURL: "https://stockbot.example",
		DeviceID:  "dev-1",
		Token:     "sbd_SECRETTOKENVALUE",
		Name:      "Counter",
		BootID:    "boot-1",
		Pending: &PendingUpdate{
			Version:  "1.2.0",
			Deadline: time.Date(2026, 10, 24, 0, 0, 0, 0, time.UTC),
		},
	}
	if err := saveState(path, state); err != nil {
		t.Fatal(err)
	}
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm() != 0o600 {
		t.Fatalf("mode = %o", info.Mode().Perm())
	}
	dirInfo, err := os.Stat(filepath.Dir(path))
	if err != nil {
		t.Fatal(err)
	}
	if dirInfo.Mode().Perm() != 0o700 {
		t.Fatalf("dir mode = %o", dirInfo.Mode().Perm())
	}
	loaded, err := loadState(path)
	if err != nil {
		t.Fatal(err)
	}
	if loaded.Token != state.Token || loaded.DeviceID != state.DeviceID || loaded.Pending.Version != "1.2.0" {
		t.Fatalf("loaded %+v", loaded)
	}
	if _, err := loadState(filepath.Join(dir, "missing.json")); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "bad.json"), []byte("{"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := loadState(filepath.Join(dir, "bad.json")); err == nil || err.Error() != "the hub credentials file is unreadable" {
		t.Fatalf("bad file error = %v", err)
	}
	empty := filepath.Join(dir, "empty.json")
	if err := os.WriteFile(empty, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	if loaded, err := loadState(empty); err != nil || loaded.Token != "" {
		t.Fatalf("empty = %+v %v", loaded, err)
	}
}

func TestBufferCapDropsOldest(t *testing.T) {
	buf := openTestBuffer(t)
	for i := 0; i < BufferCap; i++ {
		if _, err := buf.Enqueue(queuedScan{
			ID: idOf(i), Barcode: barcodeOf(i), Direction: "stock_in", UnitCount: 1, CapturedMono: int64(i), BootID: "boot",
		}); err != nil {
			t.Fatal(err)
		}
	}
	depth, err := buf.Depth()
	if err != nil || depth != BufferCap {
		t.Fatalf("depth = %d %v", depth, err)
	}
	dropped, err := buf.Dropped()
	if err != nil || dropped != 0 {
		t.Fatalf("dropped = %d %v", dropped, err)
	}
	n, err := buf.Enqueue(queuedScan{
		ID: idOf(BufferCap), Barcode: barcodeOf(BufferCap), Direction: "stock_out", UnitCount: 2, CapturedMono: int64(BufferCap), BootID: "boot",
	})
	if err != nil || n != 1 {
		t.Fatalf("drop = %d %v", n, err)
	}
	depth, _ = buf.Depth()
	dropped, _ = buf.Dropped()
	if depth != BufferCap || dropped != 1 {
		t.Fatalf("after overflow depth %d dropped %d", depth, dropped)
	}
	oldest, err := buf.Peek(1)
	if err != nil || len(oldest) != 1 || oldest[0].ID != idOf(1) || oldest[0].Barcode != barcodeOf(1) {
		t.Fatalf("oldest = %+v %v", oldest, err)
	}
	if _, err := buf.Enqueue(queuedScan{ID: idOf(2), Barcode: "x", Direction: "stock_in"}); err == nil || err.Error() != "that scan is already queued" {
		t.Fatalf("duplicate id error = %v", err)
	}
	if err := buf.AckDropped(1); err != nil {
		t.Fatal(err)
	}
	if dropped, _ = buf.Dropped(); dropped != 0 {
		t.Fatalf("ack left %d", dropped)
	}
	if err := buf.AckDropped(0); err != nil {
		t.Fatal(err)
	}
	if err := buf.Remove([]string{idOf(1)}); err != nil {
		t.Fatal(err)
	}
	if err := buf.Remove(nil); err != nil {
		t.Fatal(err)
	}
	if _, err := buf.Peek(0); err != nil {
		t.Fatal(err)
	}
	prev, up, err := buf.RotateBoot("boot")
	if err != nil || prev != "" || up != 0 {
		t.Fatalf("first boot prev %q %d %v", prev, up, err)
	}
	if err := buf.Checkpoint(80); err != nil {
		t.Fatal(err)
	}
	prev, up, err = buf.RotateBoot("boot")
	if err != nil || prev != "" || up != 0 {
		t.Fatalf("same boot prev %q %d %v", prev, up, err)
	}
	if err := buf.Checkpoint(-5); err != nil {
		t.Fatal(err)
	}
	if err := buf.Checkpoint(80); err != nil {
		t.Fatal(err)
	}
	prev, up, err = buf.RotateBoot("boot-2")
	if err != nil || prev != "boot" || up != 80 {
		t.Fatalf("rotate prev %q %d %v", prev, up, err)
	}
	if err := buf.Close(); err != nil {
		t.Fatal(err)
	}
	reopened, err := OpenBuffer(buf.path)
	if err != nil {
		t.Fatal(err)
	}
	defer reopened.Close()
	depth, _ = reopened.Depth()
	if depth != BufferCap-1 {
		t.Fatalf("reopened depth = %d", depth)
	}
	prev, up, err = reopened.RotateBoot("boot-2")
	if err != nil || prev != "boot" || up != 80 {
		t.Fatalf("reopen boot %q %d %v", prev, up, err)
	}
}

func openTestBuffer(t *testing.T) *Buffer {
	t.Helper()
	buf, err := OpenBuffer(filepath.Join(t.TempDir(), "buffer.sqlite"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = buf.Close() })
	info, err := os.Stat(buf.path)
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm() != 0o600 {
		t.Fatalf("buffer mode = %o", info.Mode().Perm())
	}
	return buf
}

func idOf(i int) string {
	return "scan-" + pad(i)
}

func barcodeOf(i int) string {
	return "9900" + pad(i)
}

func pad(i int) string {
	s := "0000"
	raw := itoaTest(i)
	if len(raw) >= len(s) {
		return raw
	}
	return s[:len(s)-len(raw)] + raw
}

func itoaTest(n int) string {
	if n == 0 {
		return "0"
	}
	var b [12]byte
	i := len(b)
	for n > 0 {
		i--
		b[i] = byte('0' + n%10)
		n /= 10
	}
	return string(b[i:])
}
