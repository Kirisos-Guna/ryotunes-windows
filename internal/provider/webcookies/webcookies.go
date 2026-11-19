// Package webcookies is the Windows source of YouTube and SoundCloud
// sessions: Ryotunes' own browser profile (the one Spotify's Web Player runs
// in), read through Ryotunes' extension and the browser's cookie API.
//
// The Linux build reads the listener's browsers' cookie stores. On Windows
// that means decrypting Chromium's cookie key with DPAPI, which Windows
// Security reports as a credential stealer (Behavior:Win32/WebBrowserCredAccess)
// and stops the daemon; Chrome, Edge and Brave also lock their cookies to
// themselves now. So on Windows the listener signs in in a window Ryotunes
// opens in its own profile instead.
package webcookies

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/ryoku-dev/ryotunes/internal/provider/browsercookies"
	"github.com/ryoku-dev/ryotunes/internal/webplayer"
)

const readLimit = 60 * time.Second

// Source implements the providers' CookieSource over Ryotunes' browser.
type Source struct {
	engine *webplayer.Engine
}

// New returns the source, or nil where there is no Ryotunes browser (Linux).
func New() *Source {
	if testing.Testing() {
		return nil // tests never start the listener's browser
	}
	engine := webplayer.Default()
	if engine == nil {
		return nil
	}
	return &Source{engine: engine}
}

func (s *Source) store() browsercookies.Store {
	return browsercookies.Store{Kind: browsercookies.Chromium, Path: s.engine.Profile()}
}

// Discover lists the one store: Ryotunes' browser profile.
func (s *Source) Discover() []browsercookies.Store { return []browsercookies.Store{s.store()} }

// Resolve accepts only Ryotunes' own profile, not a browser store remembered
// by an earlier version.
func (s *Source) Resolve(source browsercookies.SessionSource) (browsercookies.Store, error) {
	if source.Path != s.engine.Profile() {
		return browsercookies.Store{}, browsercookies.ErrInvalidSource
	}
	return s.store(), nil
}

// Read returns the profile's cookies for the given sites.
func (s *Source) Read(store browsercookies.Store, hostSuffixes ...string) ([]browsercookies.Cookie, error) {
	if store.Path != s.engine.Profile() {
		return nil, browsercookies.ErrInvalidSource
	}
	ctx, cancel := context.WithTimeout(context.Background(), readLimit)
	defer cancel()
	var out []browsercookies.Cookie
	for _, suffix := range hostSuffixes {
		cookies, err := s.engine.Cookies(ctx, strings.TrimPrefix(suffix, "."))
		if err != nil {
			return nil, err
		}
		for _, cookie := range cookies {
			converted := browsercookies.Cookie{
				Name: cookie.Name, Value: cookie.Value, Host: cookie.Domain, Path: cookie.Path,
				Partitioned: cookie.Partitioned,
			}
			if cookie.Expires > 0 {
				converted.Expires = time.Unix(int64(cookie.Expires), 0)
			}
			out = append(out, converted)
		}
	}
	return out, nil
}

// ChangedSince is always true: the providers then read the cookies again,
// which costs one message to the browser.
func (s *Source) ChangedSince(browsercookies.Store, time.Time) bool { return true }

// OpenSignIn opens a window on url in Ryotunes' browser for the listener to
// sign in; close closes it again.
func (s *Source) OpenSignIn(ctx context.Context, url string) (close func(), err error) {
	id, err := s.engine.OpenWindow(ctx, url)
	if err != nil {
		return nil, err
	}
	return func() {
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		s.engine.CloseWindow(ctx, id)
	}, nil
}

// RaiseSignIn brings an open sign-in window forward again.
func (s *Source) RaiseSignIn() { s.engine.RaiseWindows() }
