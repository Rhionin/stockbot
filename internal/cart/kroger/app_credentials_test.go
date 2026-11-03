package kroger

import "testing"

func TestApplyAndClearAppCredentials(t *testing.T) {
	a, err := New("id", "secret", "https://cb.example/kroger", "PICKUP")
	if err != nil {
		t.Fatal(err)
	}
	if err := a.ApplyAppCredentials("next", "next-secret", "https://next.example/cb", "DELIVERY"); err != nil {
		t.Fatal(err)
	}
	pub := a.PublicAppCredentials()
	if pub.ClientID != "next" || pub.Modality != "DELIVERY" || !pub.SecretSet {
		t.Fatalf("public = %+v", pub)
	}
	a.ClearAppCredentials()
	pub = a.PublicAppCredentials()
	if pub.ClientID != "" || pub.SecretSet || pub.Modality != "PICKUP" {
		t.Fatalf("after clear = %+v", pub)
	}
}

func TestApplyAppCredentialsValidation(t *testing.T) {
	a := NewUnconfigured()
	if err := a.ApplyAppCredentials("", "s", "https://cb", "PICKUP"); err == nil {
		t.Fatal("expected empty client_id error")
	}
}
