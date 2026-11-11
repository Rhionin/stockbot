package server

import (
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"regexp"
	"runtime"
	"sort"
	"strings"
	"testing"

	"github.com/Rhionin/stockbot/internal/cart"
	"github.com/stretchr/testify/require"
)

func contractTSBytes(t *testing.T) string {
	t.Helper()
	_, file, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("runtime.Caller failed")
	}
	root := filepath.Join(filepath.Dir(file), "..", "..")
	b, err := os.ReadFile(filepath.Join(root, "shared", "contract.ts"))
	require.NoError(t, err)
	return string(b)
}

func parseTSBlockFields(t *testing.T, ts, blockName string) []string {
	t.Helper()
	re := regexp.MustCompile(`(?s)` + regexp.QuoteMeta(blockName) + `\s*=\s*\{([^}]+)\}`)
	found := re.FindStringSubmatch(ts)
	require.NotNilf(t, found, "block %q not found in contract.ts", blockName)
	fieldRe := regexp.MustCompile(`^\s*([A-Za-z][A-Za-z0-9]*)\??:`)
	var fields []string
	for _, line := range strings.Split(found[1], "\n") {
		if m := fieldRe.FindStringSubmatch(strings.TrimSpace(line)); m != nil {
			fields = append(fields, m[1])
		}
	}
	sort.Strings(fields)
	return fields
}

func jsonTagsFromType(t *testing.T, typ reflect.Type) []string {
	t.Helper()
	var tags []string
	for i := 0; i < typ.NumField(); i++ {
		field := typ.Field(i)
		tag := field.Tag.Get("json")
		if tag == "" || tag == "-" {
			continue
		}
		name, _, _ := strings.Cut(tag, ",")
		if name == "" {
			name = field.Name
		}
		tags = append(tags, name)
	}
	sort.Strings(tags)
	return tags
}

func TestLastSendHandlerJSONMatchesContract(t *testing.T) {
	ts := contractTSBytes(t)
	baseFields := parseTSBlockFields(t, ts, "type LastSendBase")
	want := append([]string(nil), baseFields...)
	want = append(want, "itemId", "entryId", "status", "reason")
	sort.Strings(want)

	var sample shoppingListSendResultEntry
	got := jsonTagsFromType(t, reflect.TypeOf(sample))
	if strings.Join(got, ",") != strings.Join(want, ",") {
		t.Fatalf("handler json tags %v\nwant %v", got, want)
	}

	entryID := "e-1"
	sample = shoppingListSendResultEntry{
		ItemID:      "i-1",
		EntryID:     &entryID,
		ProviderID:  "kroger",
		Status:      cart.PublicStatusFailed,
		Reason:      cart.PublicReasonBatchRejected,
		Quantity:    2,
		AttemptedAt: "2026-11-11T14:00:00Z",
	}
	raw, err := json.Marshal(sample)
	require.NoError(t, err)
	var doc map[string]json.RawMessage
	require.NoError(t, json.Unmarshal(raw, &doc))
	var keys []string
	for k := range doc {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	if strings.Join(keys, ",") != strings.Join(want, ",") {
		t.Fatalf("marshaled keys %v\nwant %v", keys, want)
	}
}
