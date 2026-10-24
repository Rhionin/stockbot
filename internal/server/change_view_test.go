package server

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/Rhionin/stockbot/internal/scan"
	"github.com/Rhionin/stockbot/internal/tenant"
)

func TestChangeViewKeepsAFullReadWhenTheDatabaseIsMissing(t *testing.T) {
	view, err := viewChanges(context.Background(), nil, "house-north", "stock", "v1.since", "items", []string{"row"})
	if err != nil || view.Since != "" || !view.Changed {
		t.Fatalf("nil db view = %+v %v", view, err)
	}
	raw, err := (changeView{Since: "v1.since", Changed: true, Key: "items"}).RawJSON()
	if err != nil || !strings.Contains(string(raw), `"items":[]`) || !strings.Contains(string(raw), `"changed":true`) {
		t.Fatalf("nil items = %s %v", raw, err)
	}
	if !(changeView{Since: "v1.since"}).NoStore() || !(changeView{Since: "v1.since", Changed: true}).NoStore() {
		t.Fatal("a since response was cacheable")
	}
	if (changeView{}).NoStore() {
		t.Fatal("a full read was marked no-store")
	}
	quiet, err := (changeView{Since: "v1.since", Cursor: "v1.now"}).RawJSON()
	if err != nil || strings.Contains(string(quiet), "items") || !strings.Contains(string(quiet), `"changed":false`) {
		t.Fatalf("unchanged = %s %v", quiet, err)
	}
}

func TestScannerModeWithoutABookUsesTheSharedSlot(t *testing.T) {
	if (&stage1{}).db() != nil || (*stage1)(nil).db() != nil {
		t.Fatal("a stage-1 gate without a service returned a database")
	}
	if newModeBook(nil, "house-primary", nil, nil).For(context.Background(), "house-north", "hub-7") != nil {
		t.Fatal("a book without a shared mode invented a slot")
	}
	shared := newScannerMode(nil)
	book := newModeBook(shared, "house-primary", nil, nil)
	if book.For(context.Background(), "", "") != shared {
		t.Fatal("an empty household did not use the primary direct slot")
	}
	if book.For(context.Background(), "house-north", "hub-7") != nil {
		t.Fatal("an unverified device id opened a slot")
	}

	handler := &ScannerModeHandler{}
	if _, err := handler.Get(Request[struct{}, struct{}]{}); err == nil {
		t.Fatal("missing mode succeeded")
	}
	if _, err := handler.Handle(Request[scannerModeRequest, struct{}]{Body: scannerModeRequest{Mode: scan.StockOut}}); err == nil {
		t.Fatal("missing mode accepted a write")
	}
	req := httptest.NewRequest(http.MethodPost, "/api/scans/mode?deviceId=hub-7", nil)
	scope, err := tenant.New("01901111-1111-7000-8000-0000000000a1")
	if err != nil {
		t.Fatal(err)
	}
	mode := newScannerMode(nil)
	booked := &ScannerModeHandler{Mode: mode, Book: book}
	if _, err := booked.Handle(Request[scannerModeRequest, struct{}]{
		Body:       scannerModeRequest{Mode: scan.StockOut},
		RawRequest: req,
		Context:    tenant.WithScope(req.Context(), scope),
	}); err == nil {
		t.Fatal("an unknown device id was accepted")
	}
	plain := httptest.NewRequest(http.MethodPost, "/api/scans/mode", nil)
	if _, err := booked.Handle(Request[scannerModeRequest, struct{}]{
		Body:       scannerModeRequest{Mode: scan.StockOut},
		RawRequest: plain,
		Context:    tenant.WithScope(plain.Context(), scope),
	}); err != nil {
		t.Fatal(err)
	}
	got, err := booked.Get(Request[struct{}, struct{}]{RawRequest: plain, Context: tenant.WithScope(plain.Context(), scope)})
	if err != nil || got.Mode != scan.StockOut {
		t.Fatalf("get = %+v %v", got, err)
	}

	config := &ScannerConfigHandler{Mode: shared, Config: ScannerConfig{StockInBarcode: "111", StockOutBarcode: "222"}}
	shared.Set(scan.StockOut)
	view, err := config.Handle(Request[struct{}, struct{}]{})
	if err != nil || view.CurrentMode != scan.StockOut || view.StockInBarcode != "111" {
		t.Fatalf("config = %+v %v", view, err)
	}
}
