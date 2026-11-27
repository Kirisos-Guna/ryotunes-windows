package webplayer

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

func fakeWidevine(t *testing.T, dir, version string) {
	t.Helper()
	dll := filepath.Join(dir, "_platform_specific", widevinePlatform(), "widevinecdm.dll")
	if err := os.MkdirAll(filepath.Dir(dll), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(dll, []byte("cdm "+version), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "manifest.json"), []byte(`{"version":"`+version+`"}`), 0o600); err != nil {
		t.Fatal(err)
	}
}

// A new player profile gets the newest Widevine already on the PC, so the
// first songs are not silent while the browser downloads its own.
func TestSeedWidevineCopiesTheNewestOnThisPC(t *testing.T) {
	local := t.TempDir()
	t.Setenv("LOCALAPPDATA", local)
	t.Setenv("ProgramFiles", t.TempDir())
	t.Setenv("ProgramFiles(x86)", "")
	fakeWidevine(t, filepath.Join(local, `BraveSoftware\Brave-Browser`, "User Data", "WidevineCdm", "4.10.2830.0"), "4.10.2830.0")
	fakeWidevine(t, filepath.Join(local, `Google\Chrome`, "User Data", "WidevineCdm", "4.10.3112.0"), "4.10.3112.0")
	// a download the browser has not finished: no CDM in it
	if err := os.MkdirAll(filepath.Join(local, `Microsoft\Edge`, "User Data", "WidevineCdm", "9.9.9.9"), 0o700); err != nil {
		t.Fatal(err)
	}
	profile := filepath.Join(t.TempDir(), "spotify-player")
	if hasWidevine(profile) {
		t.Fatal("a new profile has Widevine")
	}
	if !seedWidevine(profile) {
		t.Fatal("seedWidevine found none to copy")
	}
	raw, err := os.ReadFile(filepath.Join(profile, "WidevineCdm", "4.10.3112.0", "_platform_specific", widevinePlatform(), "widevinecdm.dll"))
	if err != nil || string(raw) != "cdm 4.10.3112.0" {
		t.Fatalf("copied CDM = %q, %v; want the newest", raw, err)
	}
}

func TestSeedWidevineWithoutAnyOnThisPC(t *testing.T) {
	t.Setenv("LOCALAPPDATA", t.TempDir())
	t.Setenv("ProgramFiles", t.TempDir())
	t.Setenv("ProgramFiles(x86)", "")
	profile := t.TempDir()
	if seedWidevine(profile) {
		t.Fatal("seedWidevine reported Widevine with none on the PC")
	}
	e := &Engine{widevineAt: timeNowForTest()}
	if e.WidevineReady() {
		t.Fatal("a player launched without Widevine reports it ready")
	}
}

func TestNewerVersion(t *testing.T) {
	for _, c := range []struct {
		a, b string
		want bool
	}{{"4.10.3112.0", "4.10.2830.0", true}, {"4.10.2830.0", "4.10.3112.0", false}, {"4.10.1", "", true}, {"4.9.0", "4.10.0", false}} {
		if got := newerVersion(c.a, c.b); got != c.want {
			t.Errorf("newerVersion(%q, %q) = %v", c.a, c.b, got)
		}
	}
}

func timeNowForTest() time.Time { return time.Now() }
