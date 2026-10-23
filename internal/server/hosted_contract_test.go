package server

import (
	"os"
	"path/filepath"
	"regexp"
	"runtime"
	"sort"
	"strconv"
	"strings"
	"testing"

	"github.com/Rhionin/stockbot/internal/clientmsg"
	"github.com/Rhionin/stockbot/internal/inventory"
)

// Sentences the server already returns. busy and no_space are the words PR #320
// locks; those constants are not on master yet, so the literals are pinned here.
func existingContractSentences() map[string]string {
	return map[string]string{
		"busy":                          "Stockbot is busy right now. Try again in a moment.",
		"no_space":                      "Stockbot is out of storage space, so this wasn't saved.",
		inventory.CodeNotPending:        inventory.NotPendingMessage,
		inventory.CodeProductUnresolved: inventory.ProductUnresolvedMessage,
		inventory.CodeNoneOnHand:        inventory.NoneOnHandMessage,
		inventory.CodeInstanceGone:      inventory.InstanceGoneMessage,
		inventory.CodeNoItem:            inventory.WhichItemMessage,
		inventory.CodeLinkTargetGone:    inventory.LinkTargetGoneMessage,
		inventory.CodeScanNotFound:      inventory.ScanNotFoundMessage,
		inventory.CodeDirectionUnset:    inventory.DirectionUnsetMessage,
		inventory.CodeInternal:          inventory.InternalMessage,
	}
}

type contractRow struct {
	code     string
	sentence string
	notes    string
}

func contractRepoRoot(t *testing.T) string {
	t.Helper()
	_, file, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("runtime.Caller failed")
	}
	return filepath.Join(filepath.Dir(file), "..", "..")
}

func readContract(t *testing.T) (doc, ts string) {
	t.Helper()
	root := contractRepoRoot(t)
	docBytes, err := os.ReadFile(filepath.Join(root, "docs", "design", "api-contract.md"))
	if err != nil {
		t.Fatal(err)
	}
	tsBytes, err := os.ReadFile(filepath.Join(root, "frontend", "src", "api", "contract.ts"))
	if err != nil {
		t.Fatal(err)
	}
	return string(docBytes), string(tsBytes)
}

func parseRegistry(doc string) []contractRow {
	start := strings.Index(doc, "### Error code registry")
	end := strings.Index(doc, "## Open questions")
	if start < 0 || end < start {
		return nil
	}
	var rows []contractRow
	for _, line := range strings.Split(doc[start:end], "\n") {
		if !strings.HasPrefix(line, "| `") {
			continue
		}
		parts := strings.Split(line, "|")
		if len(parts) < 5 {
			continue
		}
		rows = append(rows, contractRow{
			code:     strings.Trim(parts[1], " `"),
			sentence: strings.TrimSpace(parts[3]),
			notes:    strings.TrimSpace(parts[4]),
		})
	}
	return rows
}

func parseHostedErrorCodes(ts string) []string {
	re := regexp.MustCompile(`(?s)export type HostedErrorCode =\s*([^;]+);`)
	found := re.FindStringSubmatch(ts)
	if found == nil {
		return nil
	}
	codeRe := regexp.MustCompile(`'([a-z0-9_]+)'`)
	var codes []string
	for _, match := range codeRe.FindAllStringSubmatch(found[1], -1) {
		codes = append(codes, match[1])
	}
	return codes
}

func parseEndpointIndex(doc string) []string {
	lines := strings.Split(doc, "\n")
	start := -1
	for i, line := range lines {
		if line == "endpoint-index" {
			start = i
			break
		}
	}
	if start < 0 {
		return nil
	}
	var paths []string
	for _, line := range lines[start+1:] {
		if strings.HasPrefix(line, "```") {
			break
		}
		if strings.TrimSpace(line) != "" {
			paths = append(paths, strings.TrimSpace(line))
		}
	}
	return paths
}

func TestHostedAPIContract(t *testing.T) {
	doc, ts := readContract(t)
	rows := parseRegistry(doc)
	if len(rows) == 0 {
		t.Fatal("error code registry is empty")
	}

	docCodes := make([]string, 0, len(rows))
	seen := map[string]bool{}
	for _, row := range rows {
		if seen[row.code] {
			t.Fatalf("duplicate error code %s", row.code)
		}
		seen[row.code] = true
		docCodes = append(docCodes, row.code)
	}
	tsCodes := parseHostedErrorCodes(ts)
	sort.Strings(docCodes)
	sort.Strings(tsCodes)
	if strings.Join(docCodes, ",") != strings.Join(tsCodes, ",") {
		t.Fatalf("doc codes %v\nts codes %v", docCodes, tsCodes)
	}

	existing := existingContractSentences()
	if existing["internal"] != clientmsg.ServerError {
		t.Fatalf("internal sentence %q is not clientmsg.ServerError %q", existing["internal"], clientmsg.ServerError)
	}
	for _, row := range rows {
		want, ok := existing[row.code]
		if ok {
			if row.sentence != want {
				t.Fatalf("code %s sentence %q, want %q", row.code, row.sentence, want)
			}
			continue
		}
		if !strings.Contains(row.notes, "proposed, PM to confirm") {
			t.Fatalf("new code %s is missing the PM confirmation mark", row.code)
		}
	}

	const generic = "generic-sentence: Something went wrong. Try again."
	if !strings.Contains(doc, generic) {
		t.Fatal("generic sentence line missing")
	}
	if !strings.Contains(generic, clientmsg.ServerError) {
		t.Fatalf("generic sentence does not match %q", clientmsg.ServerError)
	}

	const tenant = "tenant-rule: Nothing in this contract accepts a client-supplied tenant other than the X-Household-Id header, and that header is valid only when it names a household the signed-in user belongs to."
	if !strings.Contains(doc, tenant) {
		t.Fatal("tenant rule missing")
	}

	paths := parseEndpointIndex(doc)
	method := regexp.MustCompile(`^(GET|POST|PUT|PATCH|DELETE) /api/`)
	for _, path := range paths {
		if !method.MatchString(path) {
			t.Fatalf("bad endpoint index line %q", path)
		}
	}
	countLine := regexp.MustCompile(`(?m)^endpoint-count: (\d+)$`).FindStringSubmatch(doc)
	if countLine == nil {
		t.Fatal("endpoint-count missing")
	}
	count, err := strconv.Atoi(countLine[1])
	if err != nil {
		t.Fatal(err)
	}
	if count != len(paths) {
		t.Fatalf("endpoint-count %d, index has %d", count, len(paths))
	}
}
