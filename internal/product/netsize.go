package product

import (
	"errors"
	"math"
	"regexp"
	"strconv"
	"strings"
	"unicode"
)

// Net size is stored as grams or milliliters. The screen shows ounces or
// fluid ounces. Mass and volume are never converted into each other.
const (
	DimensionMass   = "mass"
	DimensionVolume = "volume"

	OriginOff      = "off"
	OriginManual   = "manual"
	OriginBackfill = "backfill"

	gramsPerOunce       = 28.349523125
	gramsPerPound       = 453.59237
	gramsPerKilogram    = 1000.0
	mlPerFluidOunce     = 29.5735295625
	mlPerLiter          = 1000.0
	mlPerGallon         = 128 * mlPerFluidOunce
	mlPerHalfGallon     = 64 * mlPerFluidOunce
)

// inputError is a problem with a size or pack count the person typed.
// The HTTP layer shows Error() as-is, so the text has no function names.
type inputError string

func (e inputError) Error() string { return string(e) }

// IsInputError reports whether err is a size or pack-count problem the person can fix.
func IsInputError(err error) bool {
	var input inputError
	return errors.As(err, &input)
}

func inputErr(message string) error { return inputError(message) }

// ParsedSize is a per-unit net size, plus a pack count when the text said
// how many units are in the package.
type ParsedSize struct {
	BaseValue float64
	Dimension string
	PackCount int
	HasPack   bool
}

// Longer units come first so "fl oz" is not read as "oz" and "grams" is not read as "g".
const measureUnit = `fluid\s+ounces?|fl\.?\s*oz|floz|ounces?|oz|kilograms?|kg|grams?|millilit(?:er|re)s?|ml|lit(?:er|re)s?|pounds?|lbs?|lb|half[\s-]*gallons?|gallons?|g|l`

var measurePattern = regexp.MustCompile(`(?i)(\d+(?:[.,]\d+)?)[\s]*?(` + measureUnit + `)\b`)

// A pack count can sit in the middle of a product name, as in
// "Store Brand Seltzer 6 x 12 fl oz".
var packAnywhere = regexp.MustCompile(`(?i)(\d+)\s*[x×]\s*(\d.*)`)

// ParseNetSize reads one size expression. A bare package word and an empty
// string are unknown. "6 x 14.5 oz" is a pack of six units, each 14.5 oz.
// When both an imperial measure and a metric one are present, the metric
// measure is the one stored, so "10.5 oz (298 g)" is 298 g.
func ParseNetSize(text string) (ParsedSize, bool) {
	text = strings.TrimSpace(text)
	if text == "" {
		return ParsedSize{}, false
	}
	for _, match := range packAnywhere.FindAllStringSubmatch(text, -1) {
		n, err := strconv.Atoi(match[1])
		if err != nil || n < 1 {
			continue
		}
		parsed, ok := parseMeasures(match[2])
		if !ok {
			continue
		}
		parsed.PackCount = n
		parsed.HasPack = true
		return parsed, true
	}
	return parseMeasures(text)
}

func parseMeasures(text string) (ParsedSize, bool) {
	matches := measurePattern.FindAllStringSubmatch(text, -1)
	var metric *ParsedSize
	var first *ParsedSize
	for _, match := range matches {
		amount, err := strconv.ParseFloat(strings.ReplaceAll(match[1], ",", "."), 64)
		if err != nil || amount <= 0 || math.IsNaN(amount) || math.IsInf(amount, 0) {
			continue
		}
		base, dimension, ok := measureToBase(amount, match[2])
		if !ok {
			continue
		}
		hit := ParsedSize{BaseValue: base, Dimension: dimension}
		if first == nil {
			copy := hit
			first = &copy
		}
		if dimensionFromUnit(match[2]) == metricUnit {
			copy := hit
			metric = &copy
		}
	}
	if metric != nil {
		return *metric, true
	}
	if first != nil {
		return *first, true
	}
	if base, dimension, ok := bareUnit(text); ok {
		return ParsedSize{BaseValue: base, Dimension: dimension}, true
	}
	return ParsedSize{}, false
}

const metricUnit = "metric"

func dimensionFromUnit(unit string) string {
	switch normalizeMeasureWord(unit) {
	case "g", "kg", "ml", "l":
		return metricUnit
	default:
		return ""
	}
}

func bareUnit(text string) (float64, string, bool) {
	switch normalizePhrase(text) {
	case "gallon", "gallons":
		return mlPerGallon, DimensionVolume, true
	case "half gallon", "half-gallon", "halfgallon":
		return mlPerHalfGallon, DimensionVolume, true
	default:
		return 0, "", false
	}
}

func normalizePhrase(text string) string {
	text = strings.ToLower(strings.TrimSpace(text))
	var b strings.Builder
	for _, r := range text {
		if unicode.IsLetter(r) || unicode.IsDigit(r) {
			b.WriteRune(r)
		} else if unicode.IsSpace(r) || r == '-' {
			b.WriteByte(' ')
		}
	}
	return strings.Join(strings.Fields(b.String()), " ")
}

func normalizeMeasureWord(unit string) string {
	u := strings.ToLower(strings.TrimSpace(unit))
	u = strings.ReplaceAll(u, ".", "")
	u = strings.Join(strings.Fields(u), " ")
	switch u {
	case "fl oz", "floz", "fluid oz", "fluid ounce", "fluid ounces":
		return "fl oz"
	case "oz", "ounce", "ounces":
		return "oz"
	case "lb", "lbs", "pound", "pounds":
		return "lb"
	case "kg", "kilogram", "kilograms":
		return "kg"
	case "g", "gram", "grams":
		return "g"
	case "ml", "milliliter", "milliliters", "millilitre", "millilitres":
		return "ml"
	case "l", "liter", "liters", "litre", "litres":
		return "l"
	case "gallon", "gallons":
		return "gallon"
	case "half gallon", "halfgallon":
		return "half gallon"
	default:
		if strings.HasPrefix(u, "half") && strings.Contains(u, "gallon") {
			return "half gallon"
		}
		return ""
	}
}

func measureToBase(amount float64, unit string) (float64, string, bool) {
	switch normalizeMeasureWord(unit) {
	case "oz":
		return amount * gramsPerOunce, DimensionMass, true
	case "lb":
		return amount * gramsPerPound, DimensionMass, true
	case "g":
		return amount, DimensionMass, true
	case "kg":
		return amount * gramsPerKilogram, DimensionMass, true
	case "fl oz":
		return amount * mlPerFluidOunce, DimensionVolume, true
	case "ml":
		return amount, DimensionVolume, true
	case "l":
		return amount * mlPerLiter, DimensionVolume, true
	case "gallon":
		return amount * mlPerGallon, DimensionVolume, true
	case "half gallon":
		return amount * mlPerHalfGallon, DimensionVolume, true
	default:
		return 0, "", false
	}
}

// NetSizeFromOpenFoodFacts prefers product_quantity when its unit is g or ml.
// A quantity string of "N x size" still supplies the pack count and, when
// product_quantity is the whole pack, the per-unit size is that total divided
// by N. The free-text quantity is the fallback.
func NetSizeFromOpenFoodFacts(quantity string, productQuantity float64, productQuantityUnit string) (ParsedSize, bool) {
	parsed, parsedOK := ParseNetSize(quantity)
	unit := strings.ToLower(strings.TrimSpace(productQuantityUnit))
	if productQuantity > 0 && (unit == "g" || unit == "ml") {
		dimension := DimensionMass
		if unit == "ml" {
			dimension = DimensionVolume
		}
		result := ParsedSize{BaseValue: productQuantity, Dimension: dimension}
		if parsedOK && parsed.HasPack && parsed.PackCount > 0 && parsed.Dimension == dimension {
			result.HasPack = true
			result.PackCount = parsed.PackCount
			per := parsed.BaseValue
			total := per * float64(parsed.PackCount)
			switch {
			case nearly(productQuantity, per):
				result.BaseValue = productQuantity
			case nearly(productQuantity, total):
				result.BaseValue = productQuantity / float64(parsed.PackCount)
			case productQuantity > per*1.5:
				result.BaseValue = productQuantity / float64(parsed.PackCount)
			default:
				result.BaseValue = productQuantity
			}
		}
		return result, true
	}
	return parsed, parsedOK
}

func nearly(a, b float64) bool {
	if a == 0 || b == 0 {
		return a == b
	}
	diff := math.Abs(a - b)
	scale := math.Max(math.Abs(a), math.Abs(b))
	return diff/scale <= 0.02
}

// BaseFromAmount converts a typed amount into grams or milliliters.
// unit is one of oz, fl oz, lb, g, kg, ml, L.
func BaseFromAmount(amount float64, unit string) (float64, string, error) {
	if math.IsNaN(amount) || math.IsInf(amount, 0) || amount <= 0 {
		return 0, "", inputErr("Size has to be greater than zero.")
	}
	base, dimension, ok := measureToBase(amount, unit)
	if !ok {
		return 0, "", inputErr("Choose ounces, fluid ounces, pounds, grams, kilograms, milliliters, or liters.")
	}
	return base, dimension, nil
}

// DisplayNetSize is the ounce or fluid-ounce amount the screen shows.
// The amount is rounded to one decimal, and a whole number stays whole.
func DisplayNetSize(base float64, dimension string) (amount float64, unit string, ok bool) {
	if base <= 0 || math.IsNaN(base) || math.IsInf(base, 0) {
		return 0, "", false
	}
	switch dimension {
	case DimensionMass:
		return roundTenth(base / gramsPerOunce), "oz", true
	case DimensionVolume:
		return roundTenth(base / mlPerFluidOunce), "fl oz", true
	default:
		return 0, "", false
	}
}

func roundTenth(v float64) float64 {
	return math.Round(v*10) / 10
}

// FormatQuantity is the size string shared upstream, such as "14.5 oz" or
// "6 x 14.5 oz". An unknown size is an empty string.
func FormatQuantity(base float64, dimension string, pack int) string {
	amount, unit, ok := DisplayNetSize(base, dimension)
	if !ok {
		return ""
	}
	size := formatAmount(amount) + " " + unit
	if pack > 1 {
		return strconv.Itoa(pack) + " x " + size
	}
	return size
}

func formatAmount(v float64) string {
	if v == math.Trunc(v) {
		return strconv.FormatFloat(v, 'f', 0, 64)
	}
	return strconv.FormatFloat(v, 'f', 1, 64)
}

// ApplyParsedSize stores a parsed size string on the product.
func (p *Product) ApplyParsedSize(parsed ParsedSize) {
	base := parsed.BaseValue
	p.NetBaseValue = &base
	p.NetDimension = parsed.Dimension
	p.NetSizeOrigin = OriginManual
	p.fillDisplay()
	if parsed.HasPack {
		p.PackCount = &parsed.PackCount
	}
}

// ApplyTypedSize stores a size the person typed. present is false when the
// request did not mention a size, which leaves the stored size alone.
// Clearing a size that was already saved records origin manual so a later
// backfill does not fill it in again.
func (p *Product) ApplyTypedSize(amount *float64, unit string, present, creating bool) error {
	if !present {
		return nil
	}
	unit = strings.TrimSpace(unit)
	blankAmount := amount == nil
	if blankAmount && unit == "" {
		if creating {
			p.NetBaseValue = nil
			p.NetDimension = ""
			p.NetSizeOrigin = ""
			p.NetAmount = nil
			p.NetUnit = ""
			return nil
		}
		p.NetBaseValue = nil
		p.NetDimension = ""
		p.NetSizeOrigin = OriginManual
		p.NetAmount = nil
		p.NetUnit = ""
		return nil
	}
	if blankAmount || unit == "" {
		return inputErr("Enter a size and a unit together.")
	}
	if p.sameDisplayedSize(*amount, unit) {
		p.fillDisplay()
		return nil
	}
	base, dimension, err := BaseFromAmount(*amount, unit)
	if err != nil {
		return err
	}
	p.NetBaseValue = &base
	p.NetDimension = dimension
	p.NetSizeOrigin = OriginManual
	p.fillDisplay()
	return nil
}

func (p *Product) sameDisplayedSize(amount float64, unit string) bool {
	if p.NetBaseValue == nil || p.NetDimension == "" {
		return false
	}
	shown, shownUnit, ok := DisplayNetSize(*p.NetBaseValue, p.NetDimension)
	if !ok {
		return false
	}
	return strings.EqualFold(shownUnit, strings.TrimSpace(unit)) && math.Abs(shown-amount) < 0.051
}

// ApplyTypedPack stores how many units one scan of this barcode adds.
// A missing field leaves the stored count alone. Null clears it.
func (p *Product) ApplyTypedPack(count *int, present bool) error {
	if !present {
		return nil
	}
	if count == nil {
		p.PackCount = nil
		return nil
	}
	if *count < 1 {
		return inputErr("Pack count has to be at least 1.")
	}
	n := *count
	p.PackCount = &n
	return nil
}

func (p *Product) fillDisplay() {
	if p.NetBaseValue == nil {
		p.NetAmount = nil
		p.NetUnit = ""
		return
	}
	amount, unit, ok := DisplayNetSize(*p.NetBaseValue, p.NetDimension)
	if !ok {
		p.NetAmount = nil
		p.NetUnit = ""
		return
	}
	p.NetAmount = &amount
	p.NetUnit = unit
}

// validateNetSize checks the stored triple. Unknown is all empty. A person
// can clear a size, which keeps origin manual and leaves the value empty.
// Pack count is independent and may stay unknown.
func (p Product) validateNetSize() error {
	hasBase := p.NetBaseValue != nil
	hasDim := p.NetDimension != ""
	hasOrigin := p.NetSizeOrigin != ""
	switch {
	case !hasBase && !hasDim && !hasOrigin:
	case !hasBase && !hasDim && p.NetSizeOrigin == OriginManual:
	case hasBase && hasDim && hasOrigin:
		if *p.NetBaseValue <= 0 || math.IsNaN(*p.NetBaseValue) || math.IsInf(*p.NetBaseValue, 0) {
			return inputErr("Size has to be greater than zero.")
		}
		if p.NetDimension != DimensionMass && p.NetDimension != DimensionVolume {
			return inputErr("Size has to be a weight or a volume.")
		}
		switch p.NetSizeOrigin {
		case OriginOff, OriginManual, OriginBackfill:
		default:
			return inputErr("Size could not be saved.")
		}
	default:
		return inputErr("Enter a size and a unit together.")
	}
	if p.PackCount != nil && *p.PackCount < 1 {
		return inputErr("Pack count has to be at least 1.")
	}
	return nil
}

// UpstreamQuantity is the size shared with an open database. A package word
// such as "can" is not a size, so an unknown net size contributes nothing here.
func (p Product) UpstreamQuantity() string {
	if p.NetBaseValue == nil {
		return ""
	}
	pack := 0
	if p.PackCount != nil {
		pack = *p.PackCount
	}
	return FormatQuantity(*p.NetBaseValue, p.NetDimension, pack)
}
