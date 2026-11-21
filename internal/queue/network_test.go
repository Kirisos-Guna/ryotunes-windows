package queue

import (
	"context"
	"errors"
	"fmt"
	"net"
	"testing"
	"time"

	"github.com/ryoku-dev/ryotunes/internal/provider"
)

func TestNetworkFailure(t *testing.T) {
	for _, test := range []struct {
		err  error
		want bool
	}{
		{&net.OpError{Op: "dial", Net: "tcp", Err: errors.New("connection refused")}, true},
		{fmt.Errorf("resolve: %w", context.DeadlineExceeded), true},
		{errors.New(`Get "https://music.youtube.com": dial tcp: lookup music.youtube.com: no such host`), true},
		{errors.New("Spotify's Web Player is not connected."), true},
		{provider.ErrNotFound, false},
		{provider.ErrUnavailable, false},
		{context.Canceled, false},
	} {
		if got := networkFailure(test.err); got != test.want {
			t.Errorf("networkFailure(%v) = %v, want %v", test.err, got, test.want)
		}
	}
}

// A song that fails while the network is down is tried again and plays when
// the network is back, instead of being skipped with every song after it.
func TestNetworkBlipRetriesTheSongInsteadOfSkippingIt(t *testing.T) {
	delays := networkRetryDelays
	networkRetryDelays = []time.Duration{time.Millisecond, time.Millisecond, time.Millisecond, time.Millisecond, time.Millisecond}
	t.Cleanup(func() { networkRetryDelays = delays })

	p := &fakeProvider{}
	p.networkDown.Store(2)
	q, transport := testQueue(t, p, &fakeHooks{})
	if err := q.PlayPlaylist(context.Background(), tracks("first", "second"), nil, nil, nil, false, nil); err != nil {
		t.Fatal(err)
	}
	if len(transport.loaded) != 1 || transport.loaded[0].URL != "https://stream/first" {
		t.Fatalf("loaded %v, want the first song after the network came back", transport.loaded)
	}
}

// When the network stays down, playback pauses on the song: nothing is skipped.
func TestNetworkDownPausesOnTheSong(t *testing.T) {
	delays := networkRetryDelays
	networkRetryDelays = []time.Duration{time.Millisecond, time.Millisecond}
	t.Cleanup(func() { networkRetryDelays = delays })

	p := &fakeProvider{}
	p.networkDown.Store(100)
	q, transport := testQueue(t, p, &fakeHooks{})
	if err := q.PlayPlaylist(context.Background(), tracks("first", "second"), nil, nil, nil, false, nil); err == nil {
		t.Fatal("playing with the network down succeeded")
	}
	if len(transport.loaded) != 0 {
		t.Fatalf("loaded %v with the network down", transport.loaded)
	}
	if calls := p.resolveCalls.Load(); calls != int32(len(networkRetryDelays)+1) {
		t.Fatalf("resolve calls = %d, want the first song tried %d times and the second never", calls, len(networkRetryDelays)+1)
	}
}
