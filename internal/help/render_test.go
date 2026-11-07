package help

import (
	"strings"
	"testing"
)

func TestRenderHelpMarkdownHeadingsAndLinks(t *testing.T) {
	frag := renderHelpMarkdown("## Your Wi-Fi changed\n\nSee [Your Wi-Fi changed](#your-wi-fi-changed).\n")
	if !strings.Contains(frag, `id="your-wi-fi-changed"`) {
		t.Fatalf("heading id missing: %s", frag)
	}
	if !strings.Contains(frag, `href="#your-wi-fi-changed"`) {
		t.Fatalf("internal link missing: %s", frag)
	}
}

func TestRenderHelpMarkdownCodeAndBold(t *testing.T) {
	frag := renderHelpMarkdown("Screen says `READY TO SET UP` and **STOCK OUT**.\n")
	if !strings.Contains(frag, "<code>READY TO SET UP</code>") {
		t.Fatalf("code missing: %s", frag)
	}
	if !strings.Contains(frag, "<strong>STOCK OUT</strong>") {
		t.Fatalf("bold missing: %s", frag)
	}
}
