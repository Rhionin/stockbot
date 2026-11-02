package webui

import (
	"io"
	"strings"
	"testing"
)

func TestEmbeddedAssets_ContainsPlaceholder(t *testing.T) {
	// Test that the //go:embed directive successfully embeds the placeholder

	// Open the assets subdirectory
	assetsFS, err := embedded.ReadDir("assets")
	if err != nil {
		t.Fatalf("failed to read assets directory: %v", err)
	}

	// Should contain at least index.html
	found := false
	for _, entry := range assetsFS {
		if entry.Name() == "index.html" && !entry.IsDir() {
			found = true
			break
		}
	}

	if !found {
		t.Fatal("assets directory should contain index.html file")
	}

	// Verify we can read the index.html content
	file, err := embedded.Open("assets/index.html")
	if err != nil {
		t.Fatalf("failed to open embedded assets/index.html: %v", err)
	}
	defer file.Close()

	content, err := io.ReadAll(file)
	if err != nil {
		t.Fatalf("failed to read embedded index.html: %v", err)
	}

	contentStr := string(content)
	if !strings.Contains(contentStr, "Placeholder Assets") {
		t.Error("embedded index.html should contain placeholder marker")
	}
}
