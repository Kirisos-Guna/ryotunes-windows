package spotify

import (
	"context"
	"fmt"
	"net/http"
	"os"
	"testing"

	"github.com/devgianlu/go-librespot/session"
)

// The tests cover librespot playback (Premium only) unless they say
// otherwise: on Windows the Web Player would otherwise be in use.
func TestMain(m *testing.M) {
	webPlayback = func() bool { return false }
	os.Exit(m.Run())
}

func withWebPlayback(t *testing.T) {
	t.Helper()
	previous := webPlayback
	webPlayback = func() bool { return true }
	t.Cleanup(func() { webPlayback = previous })
}

// Through the Web Player a Free account plays, and its plan is reported as
// Spotify gave it.
func TestWebPlaybackAcceptsFreeAccounts(t *testing.T) {
	withWebPlayback(t)
	client, _ := profileServer(t, func(response http.ResponseWriter, _ *http.Request) {
		fmt.Fprint(response, `{"display_name":"Fixture","product":"free"}`)
	})
	provider := newTestProvider(t, Options{DataDir: t.TempDir(), HTTPClient: client})
	profile, err := provider.premiumProfile(context.Background(), fixtureToken)
	if err != nil {
		t.Fatalf("a free account was refused: %v", err)
	}
	if !provider.installSession(new(session.Session), profile) {
		t.Fatal("session was not installed")
	}
	if status := provider.Status(); !status.SignedIn || status.Premium == nil || *status.Premium {
		t.Fatalf("status of a free account = %#v", status)
	}
}

// While Spotify has not said which plan the account has, none is reported.
func TestWebPlaybackUnknownPlan(t *testing.T) {
	withWebPlayback(t)
	provider := newTestProvider(t, Options{DataDir: t.TempDir()})
	if !provider.installSession(new(session.Session), accountProfile{DisplayName: "Fixture"}) {
		t.Fatal("session was not installed")
	}
	if status := provider.Status(); !status.SignedIn || status.Premium != nil {
		t.Fatalf("status with an unknown plan = %#v", status)
	}
}
