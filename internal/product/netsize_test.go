package product

import (
	"math"
	"testing"
)

func TestParseNetSize(t *testing.T) {
	grams := 14.5 * gramsPerOunce
	tests := []struct {
		name      string
		text      string
		ok        bool
		dimension string
		base      float64
		pack      int
		hasPack   bool
	}{
		{name: "parenthetical grams", text: "10.5 oz (298 g)", ok: true, dimension: DimensionMass, base: 298},
		{name: "ounces", text: "14.5 oz", ok: true, dimension: DimensionMass, base: grams},
		{name: "gallon", text: "gallon", ok: true, dimension: DimensionVolume, base: mlPerGallon},
		{name: "can", text: "can", ok: false},
		{name: "empty", text: "", ok: false},
		{name: "multipack", text: "6 x 14.5 oz", ok: true, dimension: DimensionMass, base: grams, pack: 6, hasPack: true},
		{name: "multipack in a name", text: "Store Brand Seltzer 6 x 12 fl oz", ok: true, dimension: DimensionVolume, base: 12 * mlPerFluidOunce, pack: 6, hasPack: true},
		{name: "half gallon", text: "half gallon", ok: true, dimension: DimensionVolume, base: mlPerHalfGallon},
		{name: "fluid ounces", text: "12 fl oz", ok: true, dimension: DimensionVolume, base: 12 * mlPerFluidOunce},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, ok := ParseNetSize(tt.text)
			if ok != tt.ok {
				t.Fatalf("ok: want %v, got %v (%+v)", tt.ok, ok, got)
			}
			if !tt.ok {
				return
			}
			if got.Dimension != tt.dimension {
				t.Errorf("dimension: want %s, got %s", tt.dimension, got.Dimension)
			}
			if math.Abs(got.BaseValue-tt.base) > 0.001 {
				t.Errorf("base: want %v, got %v", tt.base, got.BaseValue)
			}
			if got.HasPack != tt.hasPack || got.PackCount != tt.pack {
				t.Errorf("pack: want %d/%v, got %d/%v", tt.pack, tt.hasPack, got.PackCount, got.HasPack)
			}
		})
	}
}

func TestNetSizeFromOpenFoodFacts(t *testing.T) {
	got, ok := NetSizeFromOpenFoodFacts("10.5 oz (298 g)", 297.67, "g")
	if !ok || got.Dimension != DimensionMass || got.HasPack {
		t.Fatalf("campbell's: %+v ok=%v", got, ok)
	}
	if math.Abs(got.BaseValue-297.67) > 0.0001 {
		t.Fatalf("product_quantity should win, got %v", got.BaseValue)
	}

	got, ok = NetSizeFromOpenFoodFacts("6 x 12 fl oz", 0, "")
	if !ok || !got.HasPack || got.PackCount != 6 || got.Dimension != DimensionVolume {
		t.Fatalf("text fallback: %+v ok=%v", got, ok)
	}

	total := 6 * 330.0
	got, ok = NetSizeFromOpenFoodFacts("6 x 330 ml", total, "ml")
	if !ok || got.PackCount != 6 || math.Abs(got.BaseValue-330) > 0.001 {
		t.Fatalf("total split: %+v ok=%v", got, ok)
	}
}

func TestDisplayRoundTripOunces(t *testing.T) {
	base, dimension, err := BaseFromAmount(14.5, "oz")
	if err != nil {
		t.Fatal(err)
	}
	amount, unit, ok := DisplayNetSize(base, dimension)
	if !ok || unit != "oz" || amount != 14.5 {
		t.Fatalf("display = %v %s ok=%v", amount, unit, ok)
	}
}

func TestApplyParsedSizeStoresPack(t *testing.T) {
	var product Product
	parsed, ok := ParseNetSize("6 x 12 fl oz")
	if !ok {
		t.Fatal("ParseNetSize")
	}
	product.ApplyParsedSize(parsed)
	if product.NetDimension != DimensionVolume || product.PackCount == nil || *product.PackCount != 6 {
		t.Fatalf("product = %+v", product)
	}
	if product.NetSizeOrigin != OriginManual {
		t.Fatalf("origin = %q", product.NetSizeOrigin)
	}
}

func TestApplyTypedSizeKeepsManualClear(t *testing.T) {
	var product Product
	amount := 14.5
	if err := product.ApplyTypedSize(&amount, "oz", true, true); err != nil {
		t.Fatal(err)
	}
	if err := product.ApplyTypedSize(nil, "", true, false); err != nil {
		t.Fatal(err)
	}
	if product.NetSizeOrigin != OriginManual || product.NetBaseValue != nil {
		t.Fatalf("cleared = origin %s value %v", product.NetSizeOrigin, product.NetBaseValue)
	}
	if err := product.validateNetSize(); err != nil {
		t.Fatal(err)
	}
}

func TestApplyTypedSizeRejectsHalfAPair(t *testing.T) {
	var product Product
	amount := 14.5
	if err := product.ApplyTypedSize(&amount, "", true, true); err == nil || !IsInputError(err) {
		t.Fatalf("want an input error, got %v", err)
	}
}

func TestFormatQuantityIncludesPack(t *testing.T) {
	base, dimension, err := BaseFromAmount(14.5, "oz")
	if err != nil {
		t.Fatal(err)
	}
	if got := FormatQuantity(base, dimension, 6); got != "6 x 14.5 oz" {
		t.Fatalf("quantity = %q", got)
	}
}
