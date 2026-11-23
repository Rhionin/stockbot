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

func TestRenderHelpMarkdownSavedOfflineHeading(t *testing.T) {
	frag := renderHelpMarkdown("### `Saved offline` and `{N} waiting to send`\n")
	const want = `<h3 id="saved-offline-and-n-waiting-to-send"><code>Saved offline</code> and <code>{N} waiting to send</code></h3>`
	if frag != want {
		t.Fatalf("got %q, want %q", frag, want)
	}
}
