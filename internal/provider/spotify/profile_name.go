package spotify

import (
	"context"
	"encoding/json"
	"io"
	"net/url"
	"strings"
	"time"

	"github.com/devgianlu/go-librespot/session"
)

// clientDisplayName returns the account's display name when the Web API
// profile (/v1/me) did not answer, usually because librespot's shared client
// ID is rate limited there (429). It asks the client API's profile view, as
// the Spotify desktop app does, and falls back to the account ID only when
// that fails too.
func (p *Spotify) clientDisplayName(ctx context.Context, sess *session.Session) string {
	username := sess.Username()
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	resp, err := sess.Spclient().Request(ctx, "GET", "/user-profile-view/v3/profile/"+url.PathEscape(username), nil, nil, nil)
	if err != nil {
		p.log.Warn("Spotify profile view failed", "error", err)
		return username
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		p.log.Warn("Spotify profile view failed", "status", resp.StatusCode)
		return username
	}
	var view struct {
		Name string `json:"name"`
	}
	if json.NewDecoder(io.LimitReader(resp.Body, 1<<20)).Decode(&view) != nil {
		return username
	}
	if name := strings.TrimSpace(view.Name); name != "" {
		return name
	}
	return username
}
