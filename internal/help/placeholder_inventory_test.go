package help

import (
	"net/http"
	"strings"
	"testing"

	"github.com/Rhionin/stockbot/internal/app"
)

type placeholderRecord struct {
	category      string // resolved | user_copy_literal
	sourceFile    string
	sourceLine    int
	sourceLiteral string
	renderedMust  string
	renderedForbid string
}

// TestRenderedScannerHelpPlaceholderInventory renders the signed-out page body
// from the embedded v1.2 source and locks placeholder resolution.
func TestRenderedScannerHelpPlaceholderInventory(t *testing.T) {
	req, err := http.NewRequest(http.MethodGet, "/help/scanner", nil)
	if err != nil {
		t.Fatal(err)
	}
	req.Host = "pantry.test"
	body := string(buildScannerPage(app.RuntimeConfig{
		AppName:    "Northwind Pantry",
		AppBaseURL: "https://pantry.test",
	}, req))

	records := []placeholderRecord{
		{
			category:       "resolved",
			sourceFile:     "internal/help/scanner-help.md",
			sourceLine:     154,
			sourceLiteral:  "{App name}",
			renderedMust:   "Northwind Pantry · Scanner help · v1 (2026-11) · pantry.test/help/scanner",
			renderedForbid: "{App name}",
		},
		{
			category:       "resolved",
			sourceFile:     "internal/help/scanner-help.md",
			sourceLine:     154,
			sourceLiteral:  "{help host}",
			renderedMust:   "pantry.test/help/scanner",
			renderedForbid: "{help host}",
		},
		{
			category:      "user_copy_literal",
			sourceFile:    "internal/help/scanner-help.md",
			sourceLine:    84,
			sourceLiteral: "{N}",
			renderedMust:  "{N} waiting to send",
		},
	}

	for _, rec := range records {
		t.Run(rec.category+"_"+rec.sourceLiteral, func(t *testing.T) {
			if rec.renderedMust != "" && !strings.Contains(body, rec.renderedMust) {
				t.Fatalf("%s:%d rendered HTML missing %q", rec.sourceFile, rec.sourceLine, rec.renderedMust)
			}
			if rec.renderedForbid != "" && strings.Contains(body, rec.renderedForbid) {
				t.Fatalf("%s:%d rendered HTML still has unresolved %q", rec.sourceFile, rec.sourceLine, rec.renderedForbid)
			}
		})
	}

	for _, forbidden := range []string{
		"PENDING",
		"#453",
		"<!--",
		"{App name}",
		"{help host}",
		"NOT FOR PUBLISHING",
	} {
		if strings.Contains(body, forbidden) {
			t.Fatalf("public help page must not contain %q", forbidden)
		}
	}

	if !strings.Contains(body, `alt=""`) {
		t.Fatal("decorative logo must use empty alt")
	}

	const wantFooter = `<p>Northwind Pantry · Scanner help · v1 (2026-11) · pantry.test/help/scanner</p>`
	const wantNHeading = `<h3 id="saved-offline-and-n-waiting-to-send"><code>Saved offline</code> and <code>{N} waiting to send</code></h3>`
	if !strings.Contains(body, wantFooter) {
		t.Fatalf("resolved footer fragment missing: want %q", wantFooter)
	}
	if !strings.Contains(body, wantNHeading) {
		t.Fatalf("intentional {N} UI example fragment missing: want %q", wantNHeading)
	}
}

// TestRenderedEmbeddedScannerHelpDefaultConfig checks default appName in footer.
func TestRenderedEmbeddedScannerHelpDefaultConfig(t *testing.T) {
	req := httptestHelpRequest("example.test")
	body := string(buildScannerPage(app.RuntimeConfig{}, req))
	footer := "Stockbot · Scanner help · v1 (2026-11) · example.test/help/scanner"
	if !strings.Contains(body, footer) {
		t.Fatalf("default footer missing %q", footer)
	}
}

func httptestHelpRequest(host string) *http.Request {
	req, _ := http.NewRequest(http.MethodGet, "/help/scanner", nil)
	req.Host = host
	return req
}
