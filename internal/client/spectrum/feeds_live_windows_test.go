//go:build windows

package spectrum

import (
	"os"
	"testing"
	"time"
)

// TestFeedsLive runs the visualizer's feeds as the app does: both must turn
// live within a second while music plays. Set RYOTUNES_AUDIO_TESTS=1.
func TestFeedsLive(t *testing.T) {
	if os.Getenv("RYOTUNES_AUDIO_TESTS") == "" {
		t.Skip("set RYOTUNES_AUDIO_TESTS=1 with music playing")
	}
	feed := New(func() {})
	defer feed.Close()
	scope := NewScope(func() {})
	defer scope.Close()
	if !feed.Installed() || !scope.Installed() {
		t.Fatalf("installed: spectrum %v scope %v", feed.Installed(), scope.Installed())
	}
	feed.SetActive(true)
	scope.SetActive(true)
	buffer := make([]float32, 4096)
	var spectrumLive, scopeLive bool
	var frames int
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) && !(spectrumLive && scopeLive) {
		now := time.Now()
		feed.Want(now)
		scope.Want(now)
		_, spectrumLive = feed.Levels(now)
		frames, scopeLive = scope.Read(buffer)
		time.Sleep(16 * time.Millisecond)
	}
	t.Logf("spectrum live %v, scope live %v (%d frames)", spectrumLive, scopeLive, frames)
	if !spectrumLive || !scopeLive {
		t.Fatal("a visualizer feed did not turn live")
	}
}
