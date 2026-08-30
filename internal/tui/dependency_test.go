package tui

import (
	"go/parser"
	"go/token"
	"os"
	"strconv"
	"strings"
	"testing"
)

func TestPackageDoesNotImportPersistenceImplementations(t *testing.T) {
	entries, err := os.ReadDir(".")
	if err != nil {
		t.Fatal(err)
	}
	files := token.NewFileSet()
	for _, entry := range entries {
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".go") {
			continue
		}
		parsed, err := parser.ParseFile(files, entry.Name(), nil, parser.ImportsOnly)
		if err != nil {
			t.Fatal(err)
		}
		for _, imported := range parsed.Imports {
			path, err := strconv.Unquote(imported.Path.Value)
			if err != nil {
				t.Fatal(err)
			}
			if path == "github.com/antoinebaudrimont-beep/walite/internal/config" ||
				path == "github.com/antoinebaudrimont-beep/walite/internal/model" ||
				path == "github.com/antoinebaudrimont-beep/walite/internal/store" ||
				path == "github.com/antoinebaudrimont-beep/walite/internal/service" ||
				path == "github.com/antoinebaudrimont-beep/walite/internal/wa" ||
				path == "database/sql" || strings.Contains(path, "sqlite") ||
				strings.HasPrefix(path, "go.mau.fi/whatsmeow") {
				t.Fatalf("%s imports forbidden package %s", entry.Name(), path)
			}
		}
	}
}
