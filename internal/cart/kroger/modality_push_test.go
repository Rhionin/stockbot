package kroger

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"
	"testing"

	"github.com/Rhionin/stockbot/internal/cart"
)

func TestServerPushAddUsesRequestModalityOverAdapterDefault(t *testing.T) {
	tests := []struct {
		name         string
		adapterMod   string
		requestMod   string
		wantModality string
	}{
		{
			name:         "household delivery with server pickup",
			adapterMod:   "PICKUP",
			requestMod:   "DELIVERY",
			wantModality: "DELIVERY",
		},
		{
			name:         "household pickup with server delivery",
			adapterMod:   "DELIVERY",
			requestMod:   "PICKUP",
			wantModality: "PICKUP",
		},
		{
			name:         "empty request falls back to adapter default",
			adapterMod:   "DELIVERY",
			requestMod:   "",
			wantModality: "DELIVERY",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			rt := &fakeRoundTripper{resp: jsonResponse(http.StatusNoContent, "")}
			adapter, err := New("id", "secret", "https://app/cb", tt.adapterMod)
			if err != nil {
				t.Fatal(err)
			}
			adapter.WithTransport(rt)

			req := cart.ProvisionRequest{
				Provider: cart.ProviderID("kroger"),
				Lines:    []cart.ProvisionLine{{Identity: "0001111072822", Quantity: 1}},
				Modality: tt.requestMod,
			}
			if _, err := adapter.ServerPush().Add(context.Background(), cart.NewBearerCredential("tok"), req); err != nil {
				t.Fatalf("Add: %v", err)
			}

			var body struct {
				Modality string `json:"modality"`
				Items    []struct {
					Modality string `json:"modality"`
				} `json:"items"`
			}
			if err := json.Unmarshal([]byte(rt.gotBody), &body); err != nil {
				t.Fatalf("decode body: %v body=%q", err, rt.gotBody)
			}
			if body.Modality != tt.wantModality {
				t.Fatalf("top-level modality = %q want %q body=%s", body.Modality, tt.wantModality, rt.gotBody)
			}
			if len(body.Items) != 1 || body.Items[0].Modality != tt.wantModality {
				t.Fatalf("line modality = %+v want %q body=%s", body.Items, tt.wantModality, rt.gotBody)
			}
			if strings.Contains(rt.gotBody, tt.adapterMod) && tt.wantModality != tt.adapterMod {
				t.Fatalf("body still contains adapter modality %q: %s", tt.adapterMod, rt.gotBody)
			}
		})
	}
}

func TestServerPushAddIgnoresOtherHouseholdModalityInRequestOnly(t *testing.T) {
	rt := &fakeRoundTripper{resp: jsonResponse(http.StatusNoContent, "")}
	adapter, err := New("id", "secret", "https://app/cb", "PICKUP")
	if err != nil {
		t.Fatal(err)
	}
	adapter.WithTransport(rt)

	// Engine supplies modality from the scoped vault; the adapter must not invent
	// a household modality when the request leaves Modality empty.
	req := cart.ProvisionRequest{
		Provider: cart.ProviderID("kroger"),
		Lines:    []cart.ProvisionLine{{Identity: "0001111072822", Quantity: 1}},
	}
	if _, err := adapter.ServerPush().Add(context.Background(), cart.NewBearerCredential("tok"), req); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(rt.gotBody, `"modality":"PICKUP"`) {
		t.Fatalf("expected adapter fallback PICKUP, got %s", rt.gotBody)
	}
}
