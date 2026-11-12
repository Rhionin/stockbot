package cart

import (
	"os"
	"path/filepath"
	"regexp"
	"runtime"
	"sort"
	"strings"
	"testing"
)

func contractRepoRoot(t *testing.T) string {
	t.Helper()
	_, file, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("runtime.Caller failed")
	}
	return filepath.Join(filepath.Dir(file), "..", "..")
}

func expandTypeLiterals(ts, typeName string, seen map[string]bool) []string {
	if seen[typeName] {
		return nil
	}
	seen[typeName] = true
	re := regexp.MustCompile(`(?s)export type ` + regexp.QuoteMeta(typeName) + ` =\s*([^;]+);`)
	found := re.FindStringSubmatch(ts)
	if found == nil {
		return nil
	}
	body := found[1]
	lit := regexp.MustCompile(`'([a-z0-9_]+)'`)
	var out []string
	for _, m := range lit.FindAllStringSubmatch(body, -1) {
		out = append(out, m[1])
	}
	ref := regexp.MustCompile(`\b([A-Za-z][A-Za-z0-9]*)\b`)
	for _, m := range ref.FindAllStringSubmatch(body, -1) {
		name := m[1]
		if name == typeName {
			continue
		}
		out = append(out, expandTypeLiterals(ts, name, seen)...)
	}
	return out
}

func parseLastSendReasonLiterals(t *testing.T, contractTS, typesTS string) []string {
	t.Helper()
	merged := typesTS + "\n" + contractTS
	return stringSet(expandTypeLiterals(merged, "LastSendReason", map[string]bool{}))
}

func stringSet(values []string) []string {
	seen := map[string]bool{}
	var out []string
	for _, v := range values {
		if seen[v] {
			continue
		}
		seen[v] = true
		out = append(out, v)
	}
	sort.Strings(out)
	return out
}

func TestPublicStatusMatchesTypeScript(t *testing.T) {
	typesTS, err := os.ReadFile(filepath.Join(contractRepoRoot(t), "shared", "types.ts"))
	if err != nil {
		t.Fatal(err)
	}
	ts := stringSet(expandTypeLiterals(string(typesTS), "ProvisionResultStatus", map[string]bool{}))
	goVals := make([]string, 0, len(AllPublicStatuses()))
	for _, s := range AllPublicStatuses() {
		goVals = append(goVals, string(s))
	}
	sort.Strings(goVals)
	if strings.Join(goVals, ",") != strings.Join(ts, ",") {
		t.Fatalf("Go PublicStatus %v\nTS ProvisionResultStatus %v", goVals, ts)
	}
}

func TestPublicReasonMatchesTypeScript(t *testing.T) {
	root := contractRepoRoot(t)
	typesTS, err := os.ReadFile(filepath.Join(root, "shared", "types.ts"))
	if err != nil {
		t.Fatal(err)
	}
	contractTS, err := os.ReadFile(filepath.Join(root, "shared", "contract.ts"))
	if err != nil {
		t.Fatal(err)
	}
	ts := parseLastSendReasonLiterals(t, string(contractTS), string(typesTS))
	goVals := make([]string, 0, len(AllPublicReasons()))
	for _, r := range AllPublicReasons() {
		goVals = append(goVals, string(r))
	}
	sort.Strings(goVals)
	if strings.Join(goVals, ",") != strings.Join(ts, ",") {
		t.Fatalf("Go PublicReason %v\nTS LastSendReason %v", goVals, ts)
	}
}
