// Package webplayer plays Spotify through Spotify's own Web Player.
//
// Spotify no longer gives librespot clients the keys to decrypt its audio,
// and Ryotunes does not circumvent Spotify's DRM. Like Myspoti's desktop
// host, this package instead runs open.spotify.com in a browser the daemon
// starts and hides (Brave, else Microsoft Edge or Chrome, whose licensed
// Widevine module decrypts the audio), injects Myspoti's player bridge (player-bridge.js) and drives
// the page's own player through the Chrome DevTools Protocol. Plays count
// for the artist and the listener exactly as in a browser.
//
// The browser keeps its own profile in %LOCALAPPDATA%\Ryotunes\spotify-player:
// the listener signs in to Spotify once, on Spotify's own page, in a window
// the engine shows for that and hides again.
package webplayer

import (
	"context"
	_ "embed"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"
)

//go:embed player-bridge.js
var bridgeJS string

const (
	playerURL  = "https://open.spotify.com/preferences"
	loginURL   = "https://accounts.spotify.com/login?continue=https%3A%2F%2Fopen.spotify.com%2Fpreferences"
	binding    = "__mspHostPost"
	readyLimit = 45 * time.Second
)

// ErrSignInRequired means the Web Player is not signed in; the engine has
// opened its window on Spotify's sign-in page.
var ErrSignInRequired = errors.New("Sign in to Spotify in the window that just opened, then press play again.")

// Track is the page player's current item.
type Track struct {
	Kind       string `json:"kind"`
	URI        string `json:"uri"`
	Name       string `json:"name"`
	DurationMs int64  `json:"durationMs"`
}

// State is the page player's state as the bridge reports it.
type State struct {
	Track       *Track  `json:"track"`
	ContextURI  string  `json:"contextUri"`
	IsPaused    bool    `json:"isPaused"`
	IsBuffering bool    `json:"isBuffering"`
	PositionMs  float64 `json:"positionMs"`
	Timestamp   float64 `json:"timestamp"`
	DurationMs  float64 `json:"durationMs"`
}

// Position returns the playback position in seconds at time now, advancing
// the reported position while the player plays.
func (s State) Position(now time.Time) float64 {
	position := s.PositionMs
	if !s.IsPaused && !s.IsBuffering && s.Timestamp > 0 {
		elapsed := float64(now.UnixMilli()) - s.Timestamp
		if elapsed > 0 && elapsed < 6*60*60*1000 {
			position += elapsed
		}
	}
	if s.DurationMs > 0 && position > s.DurationMs {
		position = s.DurationMs
	}
	return position / 1000
}

// Engine owns the hidden browser and its player page.
type Engine struct {
	log     *slog.Logger
	profile string

	startMu sync.Mutex // serializes Start

	mu        sync.Mutex
	cmd       *exec.Cmd
	conn      *cdp
	ready     chan struct{}
	readyDone bool
	shown     bool
	state     State
	listeners []func(State)
}

var (
	defaultOnce   sync.Once
	defaultEngine *Engine
)

// Default returns the process-wide engine on Windows and nil elsewhere.
func Default() *Engine {
	defaultOnce.Do(func() {
		if _, err := findBrowser(); err != nil {
			return
		}
		local := os.Getenv("LOCALAPPDATA")
		if local == "" {
			return
		}
		defaultEngine = &Engine{log: slog.Default(), profile: filepath.Join(local, "Ryotunes", "spotify-player")}
	})
	return defaultEngine
}

// OnState registers fn for every state the page reports.
func (e *Engine) OnState(fn func(State)) {
	e.mu.Lock()
	e.listeners = append(e.listeners, fn)
	e.mu.Unlock()
}

// Running reports whether the browser is connected.
func (e *Engine) Running() bool {
	e.mu.Lock()
	defer e.mu.Unlock()
	return e.conn != nil
}

// State returns the most recent state.
func (e *Engine) State() State {
	e.mu.Lock()
	defer e.mu.Unlock()
	return e.state
}

// Start launches the browser and waits until the bridge is ready.
func (e *Engine) Start(ctx context.Context) error {
	e.startMu.Lock()
	defer e.startMu.Unlock()
	e.mu.Lock()
	running := e.conn != nil
	ready := e.ready
	e.mu.Unlock()
	if !running {
		if err := e.launch(ctx); err != nil {
			return err
		}
		e.mu.Lock()
		ready = e.ready
		e.mu.Unlock()
	}
	select {
	case <-ready:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	case <-time.After(readyLimit):
		return errors.New("Spotify's Web Player did not start. Check your connection and try again.")
	}
}

func (e *Engine) launch(ctx context.Context) error {
	browser, err := findBrowser()
	if err != nil {
		return err
	}
	if err := os.MkdirAll(e.profile, 0o700); err != nil {
		return err
	}
	if strings.EqualFold(filepath.Base(browser), "brave.exe") {
		optInWidevine(e.profile)
	}
	portFile := filepath.Join(e.profile, "DevToolsActivePort")
	_ = os.Remove(portFile)
	args := []string{
		"--remote-debugging-port=0",
		"--remote-allow-origins=http://127.0.0.1",
		"--user-data-dir=" + e.profile,
		"--no-first-run", "--no-default-browser-check",
		"--autoplay-policy=no-user-gesture-required",
		"--disable-background-timer-throttling",
		"--disable-renderer-backgrounding",
		"--disable-backgrounding-occluded-windows",
		// The window is parked out of sight and never focused: keep Chromium
		// (and Windows' efficiency mode) from throttling it, which made the
		// audio stutter.
		"--disable-features=CalculateNativeWinOcclusion,IntensiveWakeUpThrottling,UseEcoQoSForBackgroundProcess,msEfficiencyMode,msSleepingTabs",
		"--hide-crash-restore-bubble",
		"--window-size=1000,760",
	}
	args = append(args, browserLaunchArgs()...)
	args = append(args, "--app="+playerURL)
	cmd := exec.Command(browser, args...)
	detachBrowser(cmd)
	if err := cmd.Start(); err != nil {
		return fmt.Errorf("start the browser for Spotify: %w", err)
	}
	go func() { _ = cmd.Wait() }()
	stop := func() { _ = cmd.Process.Kill() }

	// Keep the window parked out of sight (off the taskbar) while the
	// browser runs: Edge can restyle or recreate it, so check every second.
	go e.keepParked(cmd)

	port, err := waitPort(ctx, portFile)
	if err != nil {
		stop()
		return err
	}
	target, err := waitPageTarget(ctx, port)
	if err != nil {
		stop()
		return err
	}
	conn, err := dialCDP(ctx, target)
	if err != nil {
		stop()
		return fmt.Errorf("connect to the Spotify player: %w", err)
	}
	e.mu.Lock()
	e.cmd, e.conn = cmd, conn
	e.ready, e.readyDone = make(chan struct{}), false
	e.mu.Unlock()
	go e.events(conn)

	setup := []struct {
		method string
		params any
	}{
		{"Runtime.enable", nil},
		{"Page.enable", nil},
		{"Runtime.addBinding", map[string]any{"name": binding}},
		{"Page.addScriptToEvaluateOnNewDocument", map[string]any{"source": bridgeJS}},
		{"Runtime.evaluate", map[string]any{"expression": bridgeJS}},
	}
	for _, step := range setup {
		if _, err := conn.send(ctx, step.method, step.params); err != nil {
			e.Close()
			return fmt.Errorf("set up the Spotify player (%s): %w", step.method, err)
		}
	}
	return nil
}

// events handles the page's CDP events until the connection closes.
func (e *Engine) events(conn *cdp) {
	for ev := range conn.events {
		switch ev.Method {
		case "Runtime.bindingCalled":
			var call struct {
				Name    string `json:"name"`
				Payload string `json:"payload"`
			}
			if json.Unmarshal(ev.Params, &call) == nil && call.Name == binding {
				e.message(call.Payload)
			}
		case "Page.frameNavigated":
			var nav struct {
				Frame struct {
					ParentID string `json:"parentId"`
				} `json:"frame"`
			}
			if json.Unmarshal(ev.Params, &nav) == nil && nav.Frame.ParentID == "" {
				e.mu.Lock()
				if e.readyDone {
					e.ready, e.readyDone = make(chan struct{}), false
				}
				e.state = State{} // a new page plays nothing yet
				e.mu.Unlock()
			}
		}
	}
	e.mu.Lock()
	e.state = State{}
	if e.conn == conn {
		e.conn, e.cmd = nil, nil
		if !e.readyDone {
			close(e.ready)
			e.readyDone = true
		}
	}
	e.mu.Unlock()
	e.log.Info("Spotify Web Player closed")
}

func (e *Engine) message(payload string) {
	var msg struct {
		Type  string          `json:"type"`
		State json.RawMessage `json:"state"`
		Error string          `json:"error"`
	}
	if json.Unmarshal([]byte(payload), &msg) != nil {
		return
	}
	switch msg.Type {
	case "ready":
		e.mu.Lock()
		if !e.readyDone {
			close(e.ready)
			e.readyDone = true
		}
		e.mu.Unlock()
	case "state":
		var state State
		if json.Unmarshal(msg.State, &state) != nil {
			return
		}
		e.mu.Lock()
		e.state = state
		listeners := append([]func(State){}, e.listeners...)
		e.mu.Unlock()
		for _, fn := range listeners {
			fn(state)
		}
	case "error", "jserror", "drm":
		e.log.Debug("Spotify Web Player", "type", msg.Type, "error", msg.Error)
	}
}

// Call runs one bridge command and returns its JSON value. While the
// sign-in window is open, it answers ErrSignInRequired at once: Spotify's
// sign-in page has no player to run commands.
func (e *Engine) Call(ctx context.Context, method string, args any) (json.RawMessage, error) {
	e.mu.Lock()
	signingIn := e.shown
	e.mu.Unlock()
	if signingIn {
		return nil, ErrSignInRequired
	}
	return e.call(ctx, method, args)
}

func (e *Engine) call(ctx context.Context, method string, args any) (json.RawMessage, error) {
	if err := e.Start(ctx); err != nil {
		return nil, err
	}
	e.mu.Lock()
	conn := e.conn
	e.mu.Unlock()
	if conn == nil {
		return nil, errors.New("Spotify's Web Player is not running.")
	}
	encoded, err := json.Marshal(args)
	if err != nil {
		return nil, err
	}
	expression := "window.Myspoti.call(" + strconv.FormatInt(time.Now().UnixNano(), 10) + "," + strconv.Quote(method) + "," + string(encoded) + ")"
	raw, err := conn.send(ctx, "Runtime.evaluate", map[string]any{
		"expression": expression, "awaitPromise": true, "returnByValue": true,
	})
	if err != nil {
		return nil, err
	}
	var result struct {
		Result struct {
			Value json.RawMessage `json:"value"`
		} `json:"result"`
		ExceptionDetails *struct {
			Text      string `json:"text"`
			Exception struct {
				Description string `json:"description"`
			} `json:"exception"`
		} `json:"exceptionDetails"`
	}
	if err := json.Unmarshal(raw, &result); err != nil {
		return nil, err
	}
	if ex := result.ExceptionDetails; ex != nil {
		text := ex.Exception.Description
		if text == "" {
			text = ex.Text
		}
		first, _, _ := strings.Cut(text, "\n")
		return nil, fmt.Errorf("Spotify's Web Player refused %s: %s", method, first)
	}
	return result.Result.Value, nil
}

// SignedIn reports whether the page has a Spotify account.
func (e *Engine) SignedIn(ctx context.Context) (bool, error) {
	e.mu.Lock()
	signingIn := e.shown
	e.mu.Unlock()
	if signingIn {
		return false, nil
	}
	return e.signedIn(ctx)
}

func (e *Engine) signedIn(ctx context.Context) (bool, error) {
	raw, err := e.call(ctx, "token.get", nil)
	if err != nil {
		return false, err
	}
	var token struct {
		AccessToken string `json:"accessToken"`
		IsAnonymous bool   `json:"isAnonymous"`
	}
	if json.Unmarshal(raw, &token) != nil {
		return false, nil
	}
	return token.AccessToken != "" && !token.IsAnonymous, nil
}

// ShowSignIn opens the window on Spotify's sign-in page and hides it again
// once the listener has signed in.
func (e *Engine) ShowSignIn(ctx context.Context) error {
	e.mu.Lock()
	open, cmd := e.shown, e.cmd
	e.mu.Unlock()
	if open && cmd != nil {
		// Already open on Spotify's sign-in page, which has no player for
		// Start to wait for: just bring it forward.
		showBrowser(cmd.Process.Pid, true)
		return nil
	}
	if err := e.Start(ctx); err != nil {
		return err
	}
	e.mu.Lock()
	conn, cmd := e.conn, e.cmd
	already := e.shown
	e.shown = true
	e.mu.Unlock()
	if conn == nil || cmd == nil {
		return errors.New("Spotify's Web Player is not running.")
	}
	if already {
		// Already open: bring it forward without reloading what is typed.
		showBrowser(cmd.Process.Pid, true)
		return nil
	}
	if _, err := conn.send(ctx, "Page.navigate", map[string]any{"url": loginURL}); err != nil {
		return err
	}
	showBrowser(cmd.Process.Pid, true)
	go e.hideAfterSignIn(cmd.Process.Pid)
	return nil
}

func (e *Engine) hideAfterSignIn(pid int) {
	deadline := time.Now().Add(15 * time.Minute)
	for time.Now().Before(deadline) {
		time.Sleep(2 * time.Second)
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		signedIn, err := e.signedIn(ctx) // the page is back on open.spotify.com once signed in
		cancel()
		if err == nil && signedIn {
			showBrowser(pid, false)
			e.mu.Lock()
			e.shown = false
			e.mu.Unlock()
			e.log.Info("Spotify Web Player signed in")
			return
		}
	}
}

// keepParked parks the browser's windows until the browser exits, except
// while the sign-in window is shown.
func (e *Engine) keepParked(cmd *exec.Cmd) {
	pid := cmd.Process.Pid
	for tick := 0; ; tick++ {
		delay := time.Second
		if tick < 40 {
			delay = 50 * time.Millisecond // the first two seconds: catch the window as it opens
		}
		time.Sleep(delay)
		e.mu.Lock()
		current, shown := e.cmd, e.shown
		e.mu.Unlock()
		if current != nil && current != cmd {
			return
		}
		if current == nil && tick > 600 {
			return
		}
		if !shown {
			showBrowser(pid, false)
		}
		if cmd.ProcessState != nil {
			return
		}
	}
}

// Close shuts the browser down.
func (e *Engine) Close() {
	e.mu.Lock()
	conn, cmd := e.conn, e.cmd
	e.conn, e.cmd = nil, nil
	e.mu.Unlock()
	if conn != nil {
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		_, _ = conn.send(ctx, "Browser.close", nil)
		cancel()
		conn.close()
	}
	if cmd != nil && cmd.Process != nil {
		_ = cmd.Process.Kill()
	}
}

// optInWidevine turns on Brave's Widevine for the player's own profile.
// Brave ships with it off and asks first; without it Spotify's encrypted
// audio does not play. Only a profile Ryotunes created is touched, and only
// to add this one setting.
func optInWidevine(profile string) {
	set := func(path string) {
		values := map[string]any{}
		if raw, err := os.ReadFile(path); err == nil {
			if json.Unmarshal(raw, &values) != nil {
				return // a file Brave is writing: leave it
			}
		}
		brave, _ := values["brave"].(map[string]any)
		if brave == nil {
			brave = map[string]any{}
		}
		if on, _ := brave["widevine_opted_in"].(bool); on {
			return
		}
		brave["widevine_opted_in"] = true
		values["brave"] = brave
		if encoded, err := json.Marshal(values); err == nil {
			_ = os.MkdirAll(filepath.Dir(path), 0o700)
			_ = os.WriteFile(path, encoded, 0o600)
		}
	}
	set(filepath.Join(profile, "Local State"))
	set(filepath.Join(profile, "Default", "Preferences"))
}

func waitPort(ctx context.Context, portFile string) (string, error) {
	deadline := time.Now().Add(20 * time.Second)
	for time.Now().Before(deadline) {
		if data, err := os.ReadFile(portFile); err == nil {
			first, _, _ := strings.Cut(strings.TrimSpace(string(data)), "\n")
			if _, err := strconv.Atoi(strings.TrimSpace(first)); err == nil {
				return strings.TrimSpace(first), nil
			}
		}
		select {
		case <-ctx.Done():
			return "", ctx.Err()
		case <-time.After(100 * time.Millisecond):
		}
	}
	return "", errors.New("the browser for Spotify did not start")
}

func waitPageTarget(ctx context.Context, port string) (string, error) {
	deadline := time.Now().Add(20 * time.Second)
	for time.Now().Before(deadline) {
		request, _ := http.NewRequestWithContext(ctx, http.MethodGet, "http://127.0.0.1:"+port+"/json", nil)
		if resp, err := http.DefaultClient.Do(request); err == nil {
			var targets []struct {
				Type                 string `json:"type"`
				URL                  string `json:"url"`
				WebSocketDebuggerURL string `json:"webSocketDebuggerUrl"`
			}
			_ = json.NewDecoder(resp.Body).Decode(&targets)
			resp.Body.Close()
			for _, target := range targets {
				if target.Type == "page" && strings.Contains(target.URL, "spotify.com") && target.WebSocketDebuggerURL != "" {
					return target.WebSocketDebuggerURL, nil
				}
			}
		}
		select {
		case <-ctx.Done():
			return "", ctx.Err()
		case <-time.After(200 * time.Millisecond):
		}
	}
	return "", errors.New("the Spotify page did not open")
}
