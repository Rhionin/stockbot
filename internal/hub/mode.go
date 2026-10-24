package hub

import (
	"time"

	"github.com/Rhionin/stockbot/internal/scan"
)

// defaultIdle matches the server's scan-in idle window. A heartbeat can
// replace it with the household's idleSeconds.
const defaultIdle = 5 * time.Minute

// mode stamps a direction on product scans. Stock-in returns to stock-out
// after the idle window with no product scan, matching the server scanner.
type mode struct {
	current    scan.ScanDirection
	idleAnchor time.Time
	idle       time.Duration
	stockIn    string
	stockOut   string
	now        func() time.Time
}

func newMode(now func() time.Time) *mode {
	if now == nil {
		now = time.Now
	}
	return &mode{
		current:    scan.StockIn,
		idleAnchor: now(),
		idle:       defaultIdle,
		stockIn:    "STOCK_IN",
		stockOut:   "STOCK_OUT",
		now:        now,
	}
}

func (m *mode) configure(stockIn, stockOut string, idle time.Duration) {
	if stockIn != "" {
		m.stockIn = stockIn
	}
	if stockOut != "" {
		m.stockOut = stockOut
	}
	if idle > 0 {
		m.idle = idle
	}
}

// control reports whether barcode switches direction. The match is exact,
// which is how the server classifies control barcodes.
func (m *mode) control(barcode string) (scan.ScanDirection, bool) {
	switch barcode {
	case m.stockIn:
		if m.stockIn == "" {
			return "", false
		}
		return scan.StockIn, true
	case m.stockOut:
		if m.stockOut == "" {
			return "", false
		}
		return scan.StockOut, true
	default:
		return "", false
	}
}

// set selects a direction. Selecting the direction already active does not
// move the idle deadline.
func (m *mode) set(d scan.ScanDirection) {
	if m.current == d {
		return
	}
	m.current = d
	m.idleAnchor = m.now()
}

// direction applies an idle return to stock-out when it is due.
func (m *mode) direction() scan.ScanDirection {
	m.applyIdle()
	return m.current
}

// noteScan postpones the return to stock-out. A scan that arrives after the
// window has elapsed is stamped stock-out and does not open a new window.
func (m *mode) noteScan() {
	if m.applyIdle() {
		return
	}
	if m.current == scan.StockIn {
		m.idleAnchor = m.now()
	}
}

func (m *mode) applyIdle() bool {
	if m.current != scan.StockIn {
		return false
	}
	idle := m.idle
	if idle <= 0 {
		idle = defaultIdle
	}
	if m.now().Sub(m.idleAnchor) < idle {
		return false
	}
	m.current = scan.StockOut
	m.idleAnchor = m.now()
	return true
}
