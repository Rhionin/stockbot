package help

import (
	"strings"
	"testing"
)

// TestEmbeddedStep5PreservesNestedOrderedList locks step 5 list shape from the
// embedded PM copy: one ordered list of five steps with nested bullets under
// steps 2 and 4, not broken into separate top-level lists.
func TestEmbeddedStep5PreservesNestedOrderedList(t *testing.T) {
	t.Helper()
	const marker = "### 5. Try it, then choose how it works"
	const end = "## If something looks wrong"
	full := string(scannerHelpMarkdown)
	start := strings.Index(full, marker)
	if start < 0 {
		t.Fatal("embedded copy missing step 5 heading")
	}
	section := full[start:]
	if endAt := strings.Index(section, end); endAt > 0 {
		section = section[:endAt]
	}
	html := renderHelpMarkdown(section)
	if strings.Count(html, "<ol>") != 1 {
		t.Fatalf("step 5 should be one <ol>, got %d: %s", strings.Count(html, "<ol>"), truncate(html, 400))
	}
	if strings.Count(html, "</ol>") != 1 {
		t.Fatalf("step 5 should close one <ol>, got %d", strings.Count(html, "</ol>"))
	}
	if strings.Count(html, "<ul>") != 2 {
		t.Fatalf("step 5 should have two nested <ul>, got %d: %s", strings.Count(html, "<ul>"), truncate(html, 600))
	}
	afterScan := strings.Index(html, "Scan two things from your pantry")
	afterLook := strings.Index(html, "Look at the screen after each scan")
	afterNest1 := strings.Index(html, "<ul>")
	afterInApp := strings.Index(html, "In the app, the scans wait")
	afterPick := strings.Index(html, "When an item is scanned")
	lastNest := strings.LastIndex(html, "<ul>")
	afterCut := strings.Index(html, "Cut out the wall card")
	if afterScan < 0 || afterLook < 0 || afterNest1 < 0 || afterInApp < 0 || afterPick < 0 || lastNest < 0 || afterCut < 0 {
		t.Fatalf("step 5 missing expected phrases in %s", truncate(html, 500))
	}
	if !(afterScan < afterLook && afterLook < afterNest1 && afterNest1 < afterInApp && afterInApp < afterPick && afterPick < lastNest && lastNest < afterCut) {
		t.Fatalf("step 5 ordering broke nested flow: scan@%d look@%d ul@%d app@%d pick@%d ul2@%d cut@%d",
			afterScan, afterLook, afterNest1, afterInApp, afterPick, lastNest, afterCut)
	}
	nest2Close := strings.Index(html[lastNest:], "</ul>")
	if nest2Close < 0 {
		t.Fatal("second nested list not closed")
	}
	afterNest2 := lastNest + nest2Close + len("</ul>")
	if strings.Index(html[afterNest2:], "</ol>") < 0 {
		t.Fatal("ordered list should continue through step 5 after nested choices")
	}
}

func truncate(s string, max int) string {
	if len(s) <= max {
		return s
	}
	return s[:max] + "..."
}
