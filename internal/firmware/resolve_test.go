package firmware

import (
	"math/rand"
	"testing"
)

func TestResolvePolicies(t *testing.T) {
	const tgt = "atoms3lite_u173_bench"
	device := func(ver, ch string) Device {
		return Device{Target: tgt, Version: ver, Channel: ch}
	}
	rel := func(id, ver, ch string, yanked bool) Release {
		return Release{ID: id, Target: tgt, Version: ver, Channel: ch, Yanked: yanked}
	}

	t.Run("policy1 pin over higher channel", func(t *testing.T) {
		d := device("1.0.0", ChannelStable)
		d.PinReleaseID = "pin-low"
		releases := []Release{
			rel("high", "2.0.0", ChannelStable, false),
			rel("pin-low", "1.0.0", ChannelStable, false),
		}
		got := Resolve(d, true, releases)
		if got == nil || got.ID != "pin-low" {
			t.Fatalf("got %#v", got)
		}
	})

	t.Run("policy1 pin while auto off", func(t *testing.T) {
		d := device("0.9.0", ChannelStable)
		d.PinReleaseID = "hold"
		releases := []Release{rel("hold", "1.5.0", ChannelStable, false)}
		got := Resolve(d, false, releases)
		if got == nil || got.ID != "hold" {
			t.Fatalf("got %#v", got)
		}
	})

	t.Run("policy1 missing pin fallthrough auto off", func(t *testing.T) {
		d := device("1.0.0", ChannelStable)
		d.PinReleaseID = "gone"
		releases := []Release{rel("a", "2.0.0", ChannelStable, false)}
		if Resolve(d, false, releases) != nil {
			t.Fatal("want nil with auto off after missing pin")
		}
	})

	t.Run("policy1 yanked pin fallthrough", func(t *testing.T) {
		d := device("1.0.0", ChannelStable)
		d.PinReleaseID = "bad"
		releases := []Release{
			rel("bad", "9.0.0", ChannelStable, true),
			rel("ok", "2.0.0", ChannelStable, false),
		}
		got := Resolve(d, true, releases)
		if got == nil || got.ID != "ok" {
			t.Fatalf("got %#v", got)
		}
	})

	t.Run("policy1 wrong target pin fallthrough", func(t *testing.T) {
		d := device("1.0.0", ChannelStable)
		d.PinReleaseID = "pin"
		releases := []Release{
			{ID: "pin", Target: "sticks3_u173", Version: "9.0.0", Channel: ChannelStable},
			rel("eligible", "2.0.0", ChannelStable, false),
		}
		got := Resolve(d, true, releases)
		if got == nil || got.ID != "eligible" {
			t.Fatalf("got %#v", got)
		}
	})

	t.Run("policy2 auto off without pin", func(t *testing.T) {
		d := device("1.0.0", ChannelStable)
		releases := []Release{rel("a", "2.0.0", ChannelStable, false)}
		if Resolve(d, false, releases) != nil {
			t.Fatal("expected nil")
		}
	})

	t.Run("policy3 stable hides beta", func(t *testing.T) {
		d := device("1.0.0", ChannelStable)
		releases := []Release{
			rel("beta", "9.0.0", ChannelBeta, false),
			rel("stable", "2.0.0", ChannelStable, false),
		}
		got := Resolve(d, true, releases)
		if got == nil || got.ID != "stable" {
			t.Fatalf("got %#v", got)
		}
	})

	t.Run("policy3 beta considers both", func(t *testing.T) {
		d := device("1.0.0", ChannelBeta)
		releases := []Release{
			rel("s", "2.0.0", ChannelStable, false),
			rel("b", "3.0.0-rc.1", ChannelBeta, false),
		}
		got := Resolve(d, true, releases)
		if got == nil || got.ID != "b" {
			t.Fatalf("got %#v", got)
		}
	})

	t.Run("policy3 yanked excluded", func(t *testing.T) {
		d := device("1.0.0", ChannelStable)
		releases := []Release{
			rel("y", "9.0.0", ChannelStable, true),
			rel("ok", "1.1.0", ChannelStable, false),
		}
		got := Resolve(d, true, releases)
		if got == nil || got.ID != "ok" {
			t.Fatalf("got %#v", got)
		}
	})

	t.Run("policy3 target isolation", func(t *testing.T) {
		d := device("1.0.0", ChannelStable)
		releases := []Release{
			{ID: "other", Target: "sticks3_u173", Version: "9.0.0", Channel: ChannelStable},
			rel("mine", "2.0.0", ChannelStable, false),
		}
		got := Resolve(d, true, releases)
		if got == nil || got.ID != "mine" {
			t.Fatalf("got %#v", got)
		}
	})

	t.Run("policy6 empty target", func(t *testing.T) {
		d := Device{Version: "1.0.0", Channel: ChannelStable}
		releases := []Release{rel("a", "2.0.0", ChannelStable, false)}
		if Resolve(d, true, releases) != nil {
			t.Fatal("expected nil")
		}
	})

	t.Run("policy4 equal current including build metadata", func(t *testing.T) {
		d := device("1.0.0+meta.a", ChannelStable)
		releases := []Release{rel("rel", "1.0.0+other", ChannelStable, false)}
		if Resolve(d, true, releases) != nil {
			t.Fatal("equal precedence should suppress offer")
		}
	})

	t.Run("policy5 rollback suppression", func(t *testing.T) {
		d := device("1.0.0", ChannelStable)
		d.RolledBackReleaseID = "bad"
		releases := []Release{rel("bad", "2.0.0", ChannelStable, false)}
		if Resolve(d, true, releases) != nil {
			t.Fatal("rolled-back release suppressed")
		}
	})

	t.Run("policy5 retry matches rollback", func(t *testing.T) {
		d := device("1.0.0", ChannelStable)
		d.RolledBackReleaseID = "bad"
		d.RetryReleaseID = "bad"
		releases := []Release{rel("bad", "2.0.0", ChannelStable, false)}
		got := Resolve(d, true, releases)
		if got == nil || got.ID != "bad" {
			t.Fatalf("got %#v", got)
		}
	})

	t.Run("policy5 no silent fallback after suppression", func(t *testing.T) {
		d := device("1.0.0", ChannelStable)
		d.RolledBackReleaseID = "top"
		releases := []Release{
			rel("top", "3.0.0", ChannelStable, false),
			rel("lower", "2.0.0", ChannelStable, false),
		}
		if Resolve(d, true, releases) != nil {
			t.Fatal("must not fall back to lower release")
		}
	})

	t.Run("tie smallest release id", func(t *testing.T) {
		d := device("1.0.0", ChannelStable)
		releases := []Release{
			rel("rel-b", "2.0.0", ChannelStable, false),
			rel("rel-a", "2.0.0", ChannelStable, false),
		}
		got := Resolve(d, true, releases)
		if got == nil || got.ID != "rel-a" {
			t.Fatalf("got %#v", got)
		}
	})

	t.Run("tie stable under input permutation", func(t *testing.T) {
		d := device("1.0.0", ChannelStable)
		base := []Release{
			rel("rel-z", "2.0.0", ChannelStable, false),
			rel("rel-a", "2.0.0", ChannelStable, false),
			rel("rel-m", "2.0.0", ChannelStable, false),
		}
		want := Resolve(d, true, base)
		for seed := int64(0); seed < 20; seed++ {
			r := rand.New(rand.NewSource(seed))
			order := append([]Release(nil), base...)
			r.Shuffle(len(order), func(i, j int) { order[i], order[j] = order[j], order[i] })
			got := Resolve(d, true, order)
			if (got == nil) != (want == nil) || (got != nil && want != nil && got.ID != want.ID) {
				t.Fatalf("seed %d: got %v want %v", seed, got, want)
			}
		}
	})
}

func TestCompareSemVerPrecedence(t *testing.T) {
	cases := []struct {
		a, b string
		want int
	}{
		{"1.0.0", "1.0.1", -1},
		{"1.0.0+build", "1.0.0+other", 0},
		{"1.0.0", "1.0.0-rc.1", 1},
		{"1.0.0-rc.2", "1.0.0-rc.10", -1},
		{"1.0.0-rc.1", "1.0.0-rc.beta", -1},
		{"1.0.0-rc.9", "1.0.0-rc.100", -1},
	}
	for _, tc := range cases {
		if got := compareSemVerPrecedence(tc.a, tc.b); got != tc.want {
			t.Fatalf("%q vs %q: got %d want %d", tc.a, tc.b, got, tc.want)
		}
	}
}
