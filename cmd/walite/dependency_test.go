package main

import (
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
)

func TestWhatsmeowDependencyStaysInsideInternalWA(t *testing.T) {
	root := filepath.Clean(filepath.Join("..", ".."))
	err := filepath.WalkDir(root, func(path string, entry os.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if entry.IsDir() {
			if entry.Name() == ".git" {
				return filepath.SkipDir
			}
			return nil
		}
		if filepath.Ext(path) != ".go" || strings.Contains(path, string(filepath.Separator)+"internal"+string(filepath.Separator)+"wa"+string(filepath.Separator)) {
			return nil
		}
		parsed, err := parser.ParseFile(token.NewFileSet(), path, nil, parser.ImportsOnly)
		if err != nil {
			return err
		}
		for _, imported := range parsed.Imports {
			value, err := strconv.Unquote(imported.Path.Value)
			if err != nil {
				return err
			}
			if strings.HasPrefix(value, "go.mau.fi/whatsmeow") {
				t.Errorf("%s imports %s outside internal/wa", path, value)
			}
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
}

func TestMilestone4BProductionCompositionUsesConnectionOwnedSourceAndSender(t *testing.T) {
	application, err := os.ReadFile("application.go")
	if err != nil {
		t.Fatal(err)
	}
	demo, err := os.ReadFile("demo.go")
	if err != nil {
		t.Fatal(err)
	}
	connected, err := os.ReadFile("connected_service.go")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(application), "connection.RealtimeSource()") ||
		!strings.Contains(string(application), "connection.TextSender()") ||
		!strings.Contains(string(application), "realtimeSource.SeedChatIDs(cachedIDs)") ||
		!strings.Contains(string(application), "newConnectedApplicationService(realtimeSource, textSender, cache)") {
		t.Fatal("production authentication does not compose the connection-owned realtime source")
	}
	if !strings.Contains(string(demo), "wa.NewOfflineTextSender") {
		t.Fatal("offline fixtures no longer construct OfflineTextSender")
	}
	if strings.Contains(string(application), "newService:    newOfflineApplicationService") ||
		strings.Contains(string(connected), "FakeSource") ||
		strings.Contains(string(connected), "OfflineTextSender") ||
		strings.Contains(string(connected), "NewMemory") ||
		!strings.Contains(string(application), "store.OpenSQLite") ||
		!strings.Contains(string(connected), "NewWithTextSender") {
		t.Fatal("connected production composition is not SQLite-only with real traffic")
	}
}

func TestMilestone4DLocalReadDoesNotCallWhatsAppReceiptOrHistoryRequestAPIs(t *testing.T) {
	root := filepath.Clean(filepath.Join("..", ".."))
	err := filepath.WalkDir(root, func(path string, entry os.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if entry.IsDir() {
			if entry.Name() == ".git" {
				return filepath.SkipDir
			}
			return nil
		}
		if filepath.Ext(path) != ".go" || strings.HasSuffix(path, "_test.go") {
			return nil
		}
		contents, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		source := string(contents)
		if strings.Contains(source, ".MarkRead(") || strings.Contains(source, "BuildHistorySyncRequest(") {
			t.Errorf("%s introduces remote read/history request behavior", path)
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
}
