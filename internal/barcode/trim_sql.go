package barcode

import "strings"

// ASCIISpaceChars is the exact character set TrimASCII strips from both ends.
// SQLTrimExpr must use the same set so legacy padded rows match trimmed probes.
const ASCIISpaceChars = " \t\n\v\f\r"

// SQLTrimExpr returns a SQLite expression that trims ASCIISpaceChars from column.
func SQLTrimExpr(column string) string {
	if column == "" {
		column = "barcode"
	}
	var b strings.Builder
	b.WriteString("trim(")
	b.WriteString(column)
	b.WriteString(", '")
	for _, r := range ASCIISpaceChars {
		if r == '\'' {
			b.WriteString("''")
		} else {
			b.WriteRune(r)
		}
	}
	b.WriteString("')")
	return b.String()
}
