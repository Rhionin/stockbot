package help

import (
	"crypto/sha256"
	"encoding/hex"
	"strings"
	"testing"

	"github.com/Rhionin/stockbot/internal/app"
)

func TestEmbeddedScannerHelpSourceHashUnchanged(t *testing.T) {
	t.Helper()
	const want = "0420fcb2866ca23e86fb13fbf0592af5f68ddbcd9c7d0760eb6beb0bc44e255a"
	sum := sha256.Sum256(scannerHelpMarkdown)
	got := hex.EncodeToString(sum[:])
	if got != want {
		t.Fatalf("embedded scanner-help.md sha256 = %s, want %s", got, want)
	}
}

func TestRenderedScannerHelpEscapesHostileAppName(t *testing.T) {
	req := httptestHelpRequest("pantry.test")
	hostile := `Northwind<script>alert(1)</script>`
	body := string(buildScannerPage(app.RuntimeConfig{
		AppName:    hostile,
		AppBaseURL: "https://pantry.test",
	}, req))

	if strings.Contains(body, hostile) {
		t.Fatalf("raw hostile app name must not appear in HTML")
	}
	const escapedFooter = `<p>Northwind&lt;script&gt;alert(1)&lt;/script&gt; · Scanner help · v1 (2026-11) · pantry.test/help/scanner</p>`
	if !strings.Contains(body, escapedFooter) {
		t.Fatalf("footer must HTML-escape APP_NAME; missing %q", escapedFooter)
	}
}

func TestRenderedScannerHelpFooterUsesConfiguredBaseURLHostNotRequestHost(t *testing.T) {
	req := httptestHelpRequest("evil.attacker.example")
	body := string(buildScannerPage(app.RuntimeConfig{
		AppName:    "Stockbot",
		AppBaseURL: "https://pantry.test",
	}, req))

	if strings.Contains(body, "evil.attacker.example/help/scanner") {
		t.Fatal("footer help host must not use request Host when APP_BASE_URL is set")
	}
	const footer = `<p>Stockbot · Scanner help · v1 (2026-11) · pantry.test/help/scanner</p>`
	if !strings.Contains(body, footer) {
		t.Fatalf("footer must use configured base URL host; want %q", footer)
	}
}

func TestRenderedScannerHelpUsesRequestHostWhenBaseURLUnset(t *testing.T) {
	req := httptestHelpRequest("lan-only.example:8080")
	body := string(buildScannerPage(app.RuntimeConfig{AppName: "Stockbot"}, req))
	const footer = `<p>Stockbot · Scanner help · v1 (2026-11) · lan-only.example:8080/help/scanner</p>`
	if !strings.Contains(body, footer) {
		t.Fatalf("when APP_BASE_URL unset, footer uses request host; want %q", footer)
	}
}
