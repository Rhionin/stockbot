package store

import "testing"

func TestCanSendToCart(t *testing.T) {
	kroger := "kroger"
	registered := func(id string) bool { return id == "kroger" || id == "other" }
	configured := func(id string) bool { return true }
	soleKroger := func() (string, bool) { return "kroger", true }
	gate := ProviderGate{Registered: registered, Configured: configured, SoleConfigured: soleKroger}

	linkedID := "store-online"
	inPersonID := "store-costco"
	c := &LineStoreContext{
		stores: []storeRow{
			{id: linkedID, linked: &kroger},
			{id: inPersonID, linked: nil},
		},
	}
	if !CanSendToCart(c, linkedID, "kroger", gate) {
		t.Fatal("linked store should send")
	}
	if CanSendToCart(c, inPersonID, "kroger", gate) {
		t.Fatal("in-person store should not send")
	}
	if !CanSendToCart(c, "", "kroger", gate) {
		t.Fatal("store-less line should send with sole linked provider")
	}

	onlyInPerson := &LineStoreContext{
		stores: []storeRow{{id: inPersonID, linked: nil}},
	}
	if !CanSendToCart(onlyInPerson, "", "kroger", gate) {
		t.Fatal("store-less line should send via sole configured provider when no store is linked")
	}

	c.stores = append(c.stores, storeRow{id: "other-online", linked: strPtr("other")})
	twoGate := ProviderGate{
		Registered:     registered,
		Configured:     configured,
		SoleConfigured: func() (string, bool) { return "", false },
	}
	if CanSendToCart(c, "", "kroger", twoGate) {
		t.Fatal("store-less should not send with two linked registered providers")
	}
	if CanSendToCart(c, "", "other", twoGate) {
		t.Fatal("store-less should not send to non-sole provider")
	}

	legacy := &LineStoreContext{}
	if !CanSendToCart(legacy, "", "kroger", gate) {
		t.Fatal("legacy household without stores keeps cart lines")
	}
}

func TestLineStoreResolutionOrder(t *testing.T) {
	kroger := "kroger"
	linked := "store-online"
	costco := "store-costco"
	c := &LineStoreContext{
		stores: []storeRow{
			{id: linked, linked: &kroger},
			{id: costco},
		},
		usual:     map[string]string{"prod-a": costco},
		defaultID: strPtr(costco),
	}
	run := linked
	if got := c.LineStore(&run, "prod-a"); got != linked {
		t.Fatalf("run override = %q, want %q", got, linked)
	}
	if got := c.LineStore(nil, "prod-a"); got != costco {
		t.Fatalf("usual store = %q, want %q", got, costco)
	}
	if got := c.LineStore(nil, "prod-other"); got != costco {
		t.Fatalf("default store = %q, want %q", got, costco)
	}
}

func strPtr(s string) *string {
	return &s
}
