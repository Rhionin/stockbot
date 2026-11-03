package server

import "testing"

func TestProviderEnvComplete(t *testing.T) {
	if (ProviderEnv{ClientID: "a", ClientSecret: "b", RedirectURI: "https://x"}).Complete() != true {
		t.Fatal("expected complete")
	}
	if (ProviderEnv{Disabled: true, ClientID: "a", ClientSecret: "b", RedirectURI: "https://x"}).Complete() {
		t.Fatal("disabled should not complete")
	}
}
