package hub

import (
	"testing"
	"time"

	"github.com/Rhionin/stockbot/internal/scan"
)

func TestModeIdle(t *testing.T) {
	now := time.Date(2026, 10, 24, 1, 0, 0, 0, time.UTC)
	m := newMode(func() time.Time { return now })
	m.configure("IN", "OUT", time.Second)
	if _, ok := m.control(""); ok {
		t.Fatal("empty barcode is not a control")
	}
	if d, ok := m.control("IN"); !ok || d != scan.StockIn {
		t.Fatalf("in = %s %v", d, ok)
	}
	m.set(scan.StockIn)
	m.noteScan()
	now = now.Add(500 * time.Millisecond)
	m.noteScan()
	if m.direction() != scan.StockIn {
		t.Fatal("scan should postpone the idle return")
	}
	now = now.Add(2 * time.Second)
	if m.direction() != scan.StockOut {
		t.Fatal("idle did not return to stock-out")
	}
	m.set(scan.StockOut)
	anchor := m.idleAnchor
	m.set(scan.StockOut)
	if !m.idleAnchor.Equal(anchor) {
		t.Fatal("repeat selection moved the idle anchor")
	}
	now = now.Add(time.Hour)
	if m.direction() != scan.StockOut {
		t.Fatal("stock-out should stay selected")
	}
	m.configure("", "", 0)
	if m.stockIn != "IN" || m.idle != time.Second {
		t.Fatal("empty config cleared the mode")
	}
	m.stockIn = ""
	m.stockOut = ""
	if _, ok := m.control(""); ok {
		t.Fatal("blank control matched")
	}
	if _, ok := m.control("OUT"); ok {
		t.Fatal("cleared stock-out still matched")
	}
	if newMode(nil).now == nil {
		t.Fatal("nil clock")
	}
}
