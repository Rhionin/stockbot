package server

import (
	"encoding/json"
	"testing"
)

func TestZeroForTripWireShape(t *testing.T) {
	z := OffListZeroForTripResponse{
		EntryID: "line-1", ProviderID: "kroger",
		ActorUserID: strPtr("u1"), ActorDisplayName: strPtr("Ada"), SetAt: strPtr("2026-11-14T00:00:00Z"),
	}
	b, err := json.Marshal(struct {
		ZeroForTrip OffListZeroForTripResponse `json:"zeroForTrip"`
	}{ZeroForTrip: z})
	if err != nil {
		t.Fatal(err)
	}
	var raw map[string]any
	if err := json.Unmarshal(b, &raw); err != nil {
		t.Fatal(err)
	}
	inner := raw["zeroForTrip"].(map[string]any)
	for _, key := range []string{"entryId", "providerId", "actorUserId", "actorDisplayName", "setAt"} {
		if _, ok := inner[key]; !ok {
			t.Fatalf("missing %q in %#v", key, inner)
		}
	}
}

func strPtr(s string) *string { return &s }