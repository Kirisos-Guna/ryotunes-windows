//go:build windows

package spectrum

import (
	"os"
	"strings"
	"sync"
	"testing"
	"time"
)

// TestLoopbackLive captures the default output for two seconds; play music
// first. Set RYOTUNES_AUDIO_TESTS=1 to run it.
func TestLoopbackLive(t *testing.T) {
	if os.Getenv("RYOTUNES_AUDIO_TESTS") == "" {
		t.Skip("set RYOTUNES_AUDIO_TESTS=1 with music playing")
	}
	analyzer := newAnalyzer()
	var mu sync.Mutex
	var frames, packets int
	var peak float32
	var last [Bands]float32
	capture, err := startLoopback(func(stereo []float32, rate int) {
		mu.Lock()
		defer mu.Unlock()
		packets++
		for _, v := range stereo {
			peak = max(peak, v, -v)
		}
		if levels, ok := analyzer.push(stereo, rate, time.Now()); ok {
			frames++
			last = levels
		}
	})
	if err != nil {
		t.Fatal(err)
	}
	time.Sleep(2 * time.Second)
	capture.stop()
	<-capture.wait
	mu.Lock()
	defer mu.Unlock()
	const shades = " .:-=+*#%@"
	var bar strings.Builder
	for _, level := range last {
		bar.WriteByte(shades[min(9, int(level*10))])
	}
	t.Logf("packets %d, spectrum frames %d, peak sample %.3f", packets, frames, peak)
	t.Logf("bars |%s|", bar.String())
	if packets == 0 {
		t.Fatal("no audio packets: is something playing?")
	}
}
