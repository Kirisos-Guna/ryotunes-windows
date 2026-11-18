package extensions

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"
)

// TestMyspotiSampleLyrics runs Myspoti's own sample lyrics extension
// (testdata/lrclib-lyrics, copied unchanged) against lrclib.net.
func TestMyspotiSampleLyrics(t *testing.T) {
	if os.Getenv("RYOTUNES_NETWORK_TESTS") == "" {
		t.Skip("set RYOTUNES_NETWORK_TESTS=1 to query lrclib.net")
	}
	manager := Load("testdata", nil)
	defer manager.Close()
	if got := len(manager.Installed()); got != 1 {
		t.Fatalf("loaded %d extensions, want 1", got)
	}
	duration := 355.0
	track := TrackFromParams("spotify:track:4u7EnebtmKWzUH433cf5Qv", "Bohemian Rhapsody", "Queen", nil, &duration)
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	lyrics, err := manager.Lyrics(ctx, track)
	if err != nil {
		t.Fatal(err)
	}
	if lyrics == nil || !lyrics.Synced || len(lyrics.Lines) < 10 {
		t.Fatalf("lyrics = %+v, want synced lines", lyrics)
	}
	t.Logf("source %q, %d lines, first %q at %d ms", lyrics.Source, len(lyrics.Lines), lyrics.Lines[0].Text, *lyrics.Lines[0].TimeMS)
}

// TestNetworkHostsEnforced refuses hosts the manifest does not name.
func TestNetworkHostsEnforced(t *testing.T) {
	dir := t.TempDir()
	ext := filepath.Join(dir, "x")
	_ = os.MkdirAll(ext, 0o700)
	_ = os.WriteFile(filepath.Join(ext, "manifest.json"), []byte(`{"id":"t.x","name":"X","actions":[{"name":"network","hosts":["lrclib.net"]}]}`), 0o600)
	_ = os.WriteFile(filepath.Join(ext, "main.js"), []byte(`myspoti.provide('lyrics', function () {
		try { myspoti.http.get('https://example.com/'); return { plain: 'leaked' }; }
		catch (e) { return { plain: String(e.message || e) }; } });`), 0o600)
	manager := Load(dir, nil)
	defer manager.Close()
	lyrics, err := manager.Lyrics(context.Background(), Track{Title: "x"})
	if err != nil || lyrics == nil || lyrics.Lines[0].Text == "leaked" {
		t.Fatalf("lyrics = %+v, err = %v; want the host refused", lyrics, err)
	}
	t.Logf("refused: %s", lyrics.Lines[0].Text)
}
