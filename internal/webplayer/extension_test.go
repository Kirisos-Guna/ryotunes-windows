package webplayer

import (
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestWriteExtension writes each extension version to a folder of its own
// and removes the folders of older versions.
func TestWriteExtension(t *testing.T) {
	root := t.TempDir()
	e := &Engine{log: slog.Default(), extension: filepath.Join(root, "spotify-player-extension")}
	for _, old := range []string{"", "-0badc0de"} {
		if err := os.MkdirAll(e.extension+old, 0o700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(e.extension+old, "background.js"), []byte("old"), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	dir, err := e.writeExtension("ws://127.0.0.1:1/ryotunes/token")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(filepath.Base(dir), "spotify-player-extension-") {
		t.Fatalf("folder %s", dir)
	}
	entries, _ := os.ReadDir(root)
	if len(entries) != 1 || entries[0].Name() != filepath.Base(dir) {
		t.Fatalf("folders left: %v", entries)
	}
	manifest, _ := os.ReadFile(filepath.Join(dir, "manifest.json"))
	if strings.Contains(string(manifest), `"version": "1.0.0"`) {
		t.Fatal("manifest version not set")
	}
	again, err := e.writeExtension("ws://127.0.0.1:2/ryotunes/token")
	if err != nil || again != dir {
		t.Fatalf("second write: %s %v", again, err)
	}
	config, _ := os.ReadFile(filepath.Join(dir, "config.json"))
	if !strings.Contains(string(config), "127.0.0.1:2") {
		t.Fatalf("config %s", config)
	}
}
