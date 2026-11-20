package help

import (
	"strings"
	"testing"

	"github.com/Rhionin/stockbot/internal/app"
)

// TestServedEmbeddedPageStructureRegressions locks U1 list nesting, U2 logo alt,
// and the v1.2 public surface on the real embedded markdown render path.
func TestServedEmbeddedPageStructureRegressions(t *testing.T) {
	req := httptestHelpRequest("pantry.test")
	body := string(buildScannerPage(app.RuntimeConfig{
		AppName:    "Northwind Pantry",
		AppBaseURL: "https://pantry.test",
	}, req))

	fragment := body[strings.Index(body, "<main>"):strings.Index(body, "</main>")]
	if strings.Count(fragment, "<h3 id=\"5-try-it-then-choose-how-it-works\">") != 1 {
		t.Fatal("step 5 heading missing")
	}
	step5 := fragment[strings.Index(fragment, "<h3 id=\"5-try-it-then-choose-how-it-works\">"):]
	if end := strings.Index(step5, "<h2 id=\"if-something-looks-wrong\">"); end > 0 {
		step5 = step5[:end]
	}
	if strings.Count(step5, "<ol>") != 1 || strings.Count(step5, "<ul>") != 2 {
		t.Fatalf("step 5 list structure: ol=%d ul=%d in %s", strings.Count(step5, "<ol>"), strings.Count(step5, "<ul>"), truncate(step5, 500))
	}
	if !strings.Contains(fragment, "Require review in Scan Queue") {
		t.Fatal("nested choice bullets missing")
	}
	if strings.Contains(fragment, `alt="Northwind Pantry"`) || !strings.Contains(body, `alt=""`) {
		t.Fatal("logo alt must stay empty")
	}
}
