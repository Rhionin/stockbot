package server

import (
	"encoding/json"
	"os"
	"path/filepath"
	"runtime"
	"strings"
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

// TestSpec12_18_ZeroForTripNullabilityOnWire — spec §12 test 18 (Go null emit).
func TestSpec12_18_ZeroForTripNullabilityOnWire(t *testing.T) {
	z := &OffListZeroForTripResponse{EntryID: "e", ProviderID: "kroger"}
	b, err := json.Marshal(struct {
		ZeroForTrip *OffListZeroForTripResponse `json:"zeroForTrip"`
	}{ZeroForTrip: z})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(b), `"actorUserId":null`) {
		t.Fatalf("actorUserId not null in %s", b)
	}
	raw := string(b)
	for _, frag := range []string{`"actorDisplayName":null`, `"setAt":null`, `"actorUserId":null`} {
		if !strings.Contains(raw, frag) {
			t.Fatalf("missing %s in %s", frag, raw)
		}
	}
}

func TestSpec12_18_ZeroForTripTypesScriptParity(t *testing.T) {
	_, file, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("caller")
	}
	root := filepath.Join(filepath.Dir(file), "..", "..")
	ts := filepath.Join(root, "shared", "types.ts")
	body, err := os.ReadFile(ts)
	if err != nil {
		t.Fatal(err)
	}
	src := string(body)
	for _, frag := range []string{
		"OffListZeroForTrip",
		"actorUserId",
		"actorDisplayName",
		"setAt",
		"zeroForTrip",
	} {
		if !strings.Contains(src, frag) {
			t.Fatalf("shared/types.ts missing %q", frag)
		}
	}
}