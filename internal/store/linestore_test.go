package store

import "testing"

func TestCanSendToCart(t *testing.T) {
	kroger := "kroger"
	registered := func(id string) bool { return id == "kroger" || id == "other" }
	linkedID := "store-online"
	inPersonID := "store-costco"
	c := &LineStoreContext{
		stores: []storeRow{
			{id: linkedID, linked: &kroger},
			{id: inPersonID, linked: nil},
		},
	}
	if !CanSendToCartForExport(c, linkedID, "kroger", registered) {
		t.Fatal("linked store should send")
	}
	if CanSendToCartForExport(c, inPersonID, "kroger", registered) {
		t.Fatal("in-person store should not send")
	}
	if !CanSendToCartForExport(c, "", "kroger", registered) {
		t.Fatal("store-less line should send with sole linked provider")
	}
	c.stores = append(c.stores, storeRow{id: "other-online", linked: strPtr("other")})
	if CanSendToCartForExport(c, "", "kroger", registered) {
		t.Fatal("store-less should not send with two linked registered providers")
	}
	if CanSendToCartForExport(c, "", "other", registered) {
		t.Fatal("store-less should not send to non-sole provider")
	}
	legacy := &LineStoreContext{}
	if !CanSendToCartForExport(legacy, "", "kroger", registered) {
		t.Fatal("legacy household without stores keeps cart lines")
	}
}

func strPtr(s string) *string {
	return &s
}
