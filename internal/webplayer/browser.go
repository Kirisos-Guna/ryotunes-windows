package webplayer

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"
)

// The player's browser also serves signing in to other services (YouTube,
// SoundCloud) in Ryotunes' own profile: Ryotunes opens a window there, the
// listener signs in, and the extension hands over that site's cookies through
// the browser's cookie API. Ryotunes does not read other browsers' cookie
// files: decrypting them is what password stealers do, and Windows Security
// stops a program that does it.

// Cookie is one cookie of Ryotunes' browser profile.
type Cookie struct {
	Name        string  `json:"name"`
	Value       string  `json:"value"`
	Domain      string  `json:"domain"`
	Path        string  `json:"path"`
	Expires     float64 `json:"expires"` // seconds since the epoch; 0 for a session cookie
	Partitioned bool    `json:"partitioned"`
}

// Profile is the browser profile folder, which identifies this cookie store.
func (e *Engine) Profile() string { return e.profile }

// Cookies returns the cookies the profile holds for domain (youtube.com or
// soundcloud.com), starting the browser if it is not running.
func (e *Engine) Cookies(ctx context.Context, domain string) ([]Cookie, error) {
	raw, err := e.browserCall(ctx, "cookies", map[string]any{"domain": domain})
	if err != nil {
		return nil, err
	}
	var cookies []Cookie
	if err := json.Unmarshal(raw, &cookies); err != nil {
		return nil, err
	}
	return cookies, nil
}

// OpenWindow opens a normal browser window on url and keeps the engine
// from parking it until CloseWindow.
func (e *Engine) OpenWindow(ctx context.Context, url string) (int64, error) {
	if err := e.connect(ctx); err != nil {
		return 0, err
	}
	// Park the player first: then the new window is the only one that
	// raiseSignInWindows brings forward.
	e.mu.Lock()
	if e.cmd != nil && e.cmd.Process != nil && !e.shown {
		showBrowser(e.cmd.Process.Pid, false)
	}
	e.windows++
	e.mu.Unlock()
	// The extension centres it on the primary monitor.
	raw, err := e.browserCall(ctx, "openWindow", map[string]any{"url": url, "width": 1000, "height": 760})
	var id int64
	if err == nil {
		err = json.Unmarshal(raw, &id)
	}
	if err != nil {
		e.mu.Lock()
		e.windows--
		e.mu.Unlock()
		return 0, err
	}
	e.mu.Lock()
	cmd := e.cmd
	e.mu.Unlock()
	if cmd != nil && cmd.Process != nil {
		pid := cmd.Process.Pid
		go func() {
			// The window can take a moment to appear.
			for range 20 {
				if raiseSignInWindows(pid) {
					return
				}
				time.Sleep(150 * time.Millisecond)
			}
		}()
	}
	return id, nil
}

// RaiseWindows brings the open sign-in windows forward again.
func (e *Engine) RaiseWindows() {
	e.mu.Lock()
	cmd, open := e.cmd, e.windows > 0
	e.mu.Unlock()
	if open && cmd != nil && cmd.Process != nil {
		raiseSignInWindows(cmd.Process.Pid)
	}
}

// CloseWindow closes a window OpenWindow opened; the player's window stays
// out of sight again.
func (e *Engine) CloseWindow(ctx context.Context, id int64) {
	if _, err := e.browserCall(ctx, "closeWindow", map[string]any{"windowId": id}); err != nil {
		e.log.Warn("could not close a sign-in window", "error", err)
	}
	e.mu.Lock()
	if e.windows > 0 {
		e.windows--
	}
	e.mu.Unlock()
}

// browserCall runs one of the extension's own commands. It needs the
// browser and the extension's connection, not a signed-in Spotify page.
func (e *Engine) browserCall(ctx context.Context, kind string, fields map[string]any) (json.RawMessage, error) {
	if err := e.connect(ctx); err != nil {
		return nil, err
	}
	result := make(chan callResult, 1)
	e.mu.Lock()
	e.nextID++
	id := e.nextID
	e.pending[id] = result
	e.mu.Unlock()
	forget := func() {
		e.mu.Lock()
		delete(e.pending, id)
		e.mu.Unlock()
	}
	message := map[string]any{"type": kind, "id": id}
	for key, value := range fields {
		message[key] = value
	}
	if err := e.send(message); err != nil {
		forget()
		return nil, err
	}
	select {
	case got := <-result:
		if got.err != nil {
			return nil, fmt.Errorf("Ryotunes' browser refused %s: %w", kind, got.err)
		}
		return got.value, nil
	case <-ctx.Done():
		forget()
		return nil, ctx.Err()
	}
}

// connect starts the browser if needed and waits for the extension to
// connect.
func (e *Engine) connect(ctx context.Context) error {
	e.startMu.Lock()
	e.mu.Lock()
	running := e.cmd != nil
	e.mu.Unlock()
	if !running {
		if err := e.launch(); err != nil {
			e.startMu.Unlock()
			return err
		}
	}
	e.startMu.Unlock()
	deadline := time.NewTimer(readyLimit)
	defer deadline.Stop()
	for {
		e.mu.Lock()
		connected, running := e.page != nil, e.cmd != nil
		e.mu.Unlock()
		if connected {
			return nil
		}
		if !running {
			return errors.New("Ryotunes' browser closed.")
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-deadline.C:
			return errors.New("Ryotunes' browser did not start. Check your connection and try again.")
		case <-time.After(100 * time.Millisecond):
		}
	}
}
