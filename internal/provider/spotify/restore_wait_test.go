package spotify

import (
	"context"
	"os"
	"testing"
	"time"
)

// A page asked for before the startup restore began waits for it, instead of
// failing with "Sign in to Spotify" until the restore lands.
func TestRequestBeforeRestoreStartsWaitsForIt(t *testing.T) {
	p := newTestProvider(t, Options{DataDir: t.TempDir()})
	if err := os.WriteFile(p.credPath, []byte(`{}`), 0o600); err != nil {
		t.Fatal(err)
	}
	waited := make(chan bool, 1)
	go func() { waited <- p.waitForRestore(context.Background()) }()
	select {
	case <-waited:
		t.Fatal("the request did not wait for the restore that had not started yet")
	case <-time.After(150 * time.Millisecond):
	}
	p.markRestored()
	select {
	case ok := <-waited:
		if !ok {
			t.Fatal("waitForRestore = false after the restore finished")
		}
	case <-time.After(2 * time.Second):
		t.Fatal("the request kept waiting after the restore finished")
	}
}
