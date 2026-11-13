package backup

import (
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"runtime"
	"strconv"
	"strings"
	"testing"
)

var receiptParserFuncs = []string{
	"receipt_json_field_str",
	"receipt_json_field_bool",
	"receipt_json_field_num",
	"receipt_parse_container_output",
}

func repoRoot(t *testing.T) string {
	t.Helper()
	_, file, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("caller")
	}
	return filepath.Clean(filepath.Join(filepath.Dir(file), "..", ".."))
}

func extractBashFunction(source, name string) (string, error) {
	needle := name + "() {"
	idx := strings.Index(source, needle)
	if idx < 0 {
		return "", fmt.Errorf("missing function %q", name)
	}
	brace := strings.Index(source[idx:], "{")
	if brace < 0 {
		return "", fmt.Errorf("missing opening brace for %q", name)
	}
	pos := idx + brace
	depth := 0
	for i := pos; i < len(source); i++ {
		switch source[i] {
		case '{':
			depth++
		case '}':
			depth--
			if depth == 0 {
				return source[idx : i+1], nil
			}
		}
	}
	return "", fmt.Errorf("unclosed function %q", name)
}

func extractReceiptParserFromScript(t *testing.T) string {
	t.Helper()
	if _, err := exec.LookPath("bash"); err != nil {
		t.Skip("bash not on PATH")
	}
	path := filepath.Join(repoRoot(t), "deploy", "systemd", "pantry-backup.sh")
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read wrapper script: %v", err)
	}
	source := string(raw)
	var parts []string
	for _, name := range receiptParserFuncs {
		fn, err := extractBashFunction(source, name)
		if err != nil {
			t.Fatalf("%v", err)
		}
		parts = append(parts, fn)
	}
	return strings.Join(parts, "\n\n")
}

type parsedReceipt struct {
	ResultSource      string
	CiphertextSHA256  string
	CiphertextSize    string
	ManifestKey       string
	ToolVersion       string
	ObjectKey         string
	ErrorClass        string
}

func parseStdoutWithWrapperParser(t *testing.T, parserSrc, stdoutPath, stderrPath string) parsedReceipt {
	t.Helper()
	script := parserSrc + `
receipt_parse_container_output "$1" "$2"
printf 'result_source=%s\n' "$receipt_result_source"
printf 'ciphertext_sha256=%s\n' "$receipt_go_ciphertext_sha256"
printf 'ciphertext_size=%s\n' "$receipt_go_ciphertext_size"
printf 'manifest_key=%s\n' "$receipt_go_manifest_key"
printf 'tool_version=%s\n' "$receipt_go_tool_version"
printf 'object_key=%s\n' "$receipt_go_object_key"
printf 'error_class=%s\n' "$receipt_go_error_class"
`
	cmd := exec.Command("bash", "-c", script, "_", stdoutPath, stderrPath)
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("bash parser: %v\n%s", err, out)
	}
	var pr parsedReceipt
	for _, line := range strings.Split(strings.TrimSpace(string(out)), "\n") {
		key, val, ok := strings.Cut(line, "=")
		if !ok {
			continue
		}
		switch key {
		case "result_source":
			pr.ResultSource = val
		case "ciphertext_sha256":
			pr.CiphertextSHA256 = val
		case "ciphertext_size":
			pr.CiphertextSize = val
		case "manifest_key":
			pr.ManifestKey = val
		case "tool_version":
			pr.ToolVersion = val
		case "object_key":
			pr.ObjectKey = val
		case "error_class":
			pr.ErrorClass = val
		}
	}
	return pr
}

func backupResultJSON(stdout string) map[string]any {
	for _, line := range strings.Split(stdout, "\n") {
		if !strings.HasPrefix(line, "backup result ") {
			continue
		}
		var m map[string]any
		if err := json.Unmarshal([]byte(strings.TrimPrefix(line, "backup result ")), &m); err != nil {
			panic(err)
		}
		return m
	}
	return nil
}

func buildBackupBinary(t *testing.T, dir string) string {
	t.Helper()
	out := filepath.Join(dir, "stockbot-backup")
	cmd := exec.Command("go", "build", "-o", out, "./cmd/backup")
	cmd.Dir = repoRoot(t)
	if combined, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("go build backup: %v\n%s", err, combined)
	}
	return out
}

func runBuiltBackup(t *testing.T, bin string, args []string) (stdout, stderr string, code int) {
	t.Helper()
	cmd := exec.Command(bin, args...)
	var outBuf, errBuf strings.Builder
	cmd.Stdout = &outBuf
	cmd.Stderr = &errBuf
	err := cmd.Run()
	if err != nil {
		if ee, ok := err.(*exec.ExitError); ok {
			code = ee.ExitCode()
		} else {
			t.Fatalf("run backup: %v", err)
		}
	}
	return outBuf.String(), errBuf.String(), code
}

func TestWrapperParserAcceptsRealBackupBinaryStdout(t *testing.T) {
	parser := extractReceiptParserFromScript(t)
	settings, recipients := cliRunFixture(t)
	db := fixtureDB(t)
	bin := buildBackupBinary(t, t.TempDir())
	stdout, stderr, code := runBuiltBackup(t, bin, []string{
		"run",
		"-db", db,
		"-stage", t.TempDir(),
		"-tmpfs-limit", "1000000000",
		"-secrets", settings,
		"-recipients", recipients,
		"-reason", "manual",
		"-busy-timeout", "1s",
		"-busy-retries", "0",
	})
	if code != ExitOK {
		t.Fatalf("exit %d\nstdout=%q\nstderr=%q", code, stdout, stderr)
	}
	work := t.TempDir()
	stdoutFile := filepath.Join(work, "stdout")
	stderrFile := filepath.Join(work, "stderr")
	if err := os.WriteFile(stdoutFile, []byte(stdout), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(stderrFile, []byte(stderr), 0o600); err != nil {
		t.Fatal(err)
	}
	pr := parseStdoutWithWrapperParser(t, parser, stdoutFile, stderrFile)
	if pr.ResultSource != "go" {
		t.Fatalf("result_source = %q, want go\nstdout:\n%s", pr.ResultSource, stdout)
	}
	result := backupResultJSON(stdout)
	if result == nil {
		t.Fatal("missing backup result json in stdout")
	}
	wantSHA, _ := result["ciphertext_sha256"].(string)
	if !sha256Hex.MatchString(pr.CiphertextSHA256) || pr.CiphertextSHA256 != wantSHA {
		t.Fatalf("ciphertext_sha256 parser=%q json=%q", pr.CiphertextSHA256, wantSHA)
	}
	wantSize, _ := result["ciphertext_size"].(float64)
	gotSize, err := strconv.ParseInt(pr.CiphertextSize, 10, 64)
	if err != nil || gotSize <= 0 || int64(wantSize) != gotSize {
		t.Fatalf("ciphertext_size parser=%q json=%v", pr.CiphertextSize, wantSize)
	}
	if pr.ManifestKey == "" || pr.ToolVersion == "" {
		t.Fatalf("manifest_key=%q tool_version=%q", pr.ManifestKey, pr.ToolVersion)
	}
	wantKey, _ := result["object_key"].(string)
	if pr.ObjectKey != wantKey {
		t.Fatalf("object_key parser=%q json=%q", pr.ObjectKey, wantKey)
	}
	if !regexp.MustCompile(`backup result `).MatchString(stdout) {
		t.Fatal("stdout missing backup result line")
	}
	okAt := strings.Index(stdout, "backup ok key=")
	resAt := strings.Index(stdout, "backup result ")
	if resAt < 0 || okAt < 0 || resAt >= okAt {
		t.Fatalf("order: result@%d ok@%d\n%s", resAt, okAt, stdout)
	}
}

func TestWrapperParserFailureStdout(t *testing.T) {
	parser := extractReceiptParserFromScript(t)
	settings, recipients := cliRunFixture(t)
	bin := buildBackupBinary(t, t.TempDir())
	stdout, stderr, code := runBuiltBackup(t, bin, []string{
		"run",
		"-db", filepath.Join(t.TempDir(), "missing.db"),
		"-stage", t.TempDir(),
		"-secrets", settings,
		"-recipients", recipients,
		"-busy-timeout", "1s",
		"-busy-retries", "0",
	})
	if code == ExitOK {
		t.Fatal("missing database succeeded")
	}
	work := t.TempDir()
	stdoutFile := filepath.Join(work, "stdout")
	stderrFile := filepath.Join(work, "stderr")
	if err := os.WriteFile(stdoutFile, []byte(stdout), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(stderrFile, []byte(stderr), 0o600); err != nil {
		t.Fatal(err)
	}
	pr := parseStdoutWithWrapperParser(t, parser, stdoutFile, stderrFile)
	if pr.ResultSource != "go" {
		t.Fatalf("result_source = %q, want go", pr.ResultSource)
	}
	result := backupResultJSON(stdout)
	if result == nil {
		t.Fatal("missing backup result json")
	}
	class, _ := result["error_class"].(string)
	if class == "" {
		t.Fatalf("missing error_class in json: %#v", result)
	}
	if pr.ErrorClass != class {
		t.Fatalf("error_class parser=%q json=%q", pr.ErrorClass, class)
	}
}
