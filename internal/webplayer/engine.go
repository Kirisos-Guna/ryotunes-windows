// Package webplayer plays Spotify through Spotify's own Web Player.
//
// Spotify no longer gives librespot clients the keys to decrypt its audio,
// and Ryotunes does not circumvent Spotify's DRM. Like Myspoti, this package
// instead runs open.spotify.com in a browser the daemon starts and hides
// (Brave, else Microsoft Edge or Chrome, whose licensed Widevine module
// decrypts the audio) and drives the page's own player through Myspoti's
// player bridge (extension/player-bridge.js). Plays count for the artist and
// the listener exactly as in a browser.
//
// The browser is not automated: Spotify refuses licences to a page that says
// it is controlled by automation (remote debugging sets navigator.webdriver),
// and then skips to other songs. As Myspoti's Android host injects the bridge
// into its WebView, Ryotunes loads a small extension of its own (extension/)
// into the player's profile: it injects the bridge and relays its messages
// over a local WebSocket, which only a holder of the per-launch token can use.
//
// The browser keeps its own profile in %LOCALAPPDATA%\Ryotunes\spotify-player:
// the listener signs in to Spotify once, on Spotify's own page, in a window
// the engine shows for that and hides again.
package webplayer

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"embed"
	"encoding/binary"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"log/slog"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/gorilla/websocket"
)

//go:embed extension
var extensionFiles embed.FS

const (
	playerURL  = "https://open.spotify.com/preferences"
	loginURL   = "https://accounts.spotify.com/login?continue=https%3A%2F%2Fopen.spotify.com%2Fpreferences"
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
	log       *slog.Logger
	profile   string
	extension string

	startMu sync.Mutex // serializes Start

	mu        sync.Mutex
	cmd       *exec.Cmd
	exited    chan struct{}
	server    *http.Server
	token     string
	page      *websocket.Conn
	writeMu   sync.Mutex
	ready     chan struct{}
	readyDone bool
	shown     bool
	windows   int // sign-in windows OpenWindow opened: not parked
	state     State
	listeners []func(State)
	nextID    int64
	pending   map[int64]chan callResult
}

type callResult struct {
	value json.RawMessage
	err   error
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
		root := filepath.Join(local, "Ryotunes")
		defaultEngine = &Engine{
			log: slog.Default(), profile: filepath.Join(root, "spotify-player"),
			extension: filepath.Join(root, "spotify-player-extension"),
			pending:   map[int64]chan callResult{},
		}
	})
	return defaultEngine
}

// OnState registers fn for every state the page reports.
func (e *Engine) OnState(fn func(State)) {
	e.mu.Lock()
	e.listeners = append(e.listeners, fn)
	e.mu.Unlock()
}

// Running reports whether the browser is running.
func (e *Engine) Running() bool {
	e.mu.Lock()
	defer e.mu.Unlock()
	return e.cmd != nil
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
	running := e.cmd != nil
	ready := e.ready
	e.mu.Unlock()
	if !running {
		if err := e.launch(); err != nil {
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

// launch starts the relay server, writes the extension with its address
// and starts the browser with it.
func (e *Engine) launch() error {
	browser, err := findBrowser()
	if err != nil {
		return err
	}
	if err := os.MkdirAll(e.profile, 0o700); err != nil {
		return err
	}
	closeLeftovers(e.profile)
	if strings.EqualFold(filepath.Base(browser), "brave.exe") {
		optInWidevine(e.profile)
	}

	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return fmt.Errorf("start the Spotify player relay: %w", err)
	}
	token := randomToken()
	address := "ws://" + listener.Addr().String() + "/ryotunes/" + token
	extension, err := e.writeExtension(address)
	if err != nil {
		listener.Close()
		return fmt.Errorf("prepare the Spotify player extension: %w", err)
	}
	server := &http.Server{Handler: http.HandlerFunc(e.serveRelay), ReadHeaderTimeout: 10 * time.Second}
	go func() { _ = server.Serve(listener) }()

	args := []string{
		"--user-data-dir=" + e.profile,
		"--load-extension=" + extension,
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
		server.Close()
		return fmt.Errorf("start the browser for Spotify: %w", err)
	}
	exited := make(chan struct{})
	e.mu.Lock()
	e.cmd, e.exited, e.server, e.token = cmd, exited, server, token
	e.ready, e.readyDone = make(chan struct{}), false
	e.mu.Unlock()
	go func() {
		_ = cmd.Wait()
		close(exited)
		e.browserExited(cmd)
	}()
	// Keep the window parked out of sight (off the taskbar) while the
	// browser runs: the browser can restyle or recreate it.
	go e.keepParked(cmd)
	return nil
}

// writeExtension writes the extension, with the relay's address, and returns
// the folder the browser loads it from.
//
// Each version of the extension goes in a folder of its own: the browser
// keeps an unpacked extension's service worker, cached in the profile, for as
// long as the extension's folder stays the same, so a Ryotunes update would go
// on running the old background script. A new folder is a new extension to
// the browser. Folders of older versions are removed.
func (e *Engine) writeExtension(address string) (string, error) {
	digest := sha256.New()
	files := map[string][]byte{}
	err := fs.WalkDir(extensionFiles, "extension", func(path string, entry fs.DirEntry, err error) error {
		if err != nil || entry.IsDir() {
			return err
		}
		data, err := extensionFiles.ReadFile(path)
		if err != nil {
			return err
		}
		files[filepath.Base(path)] = data
		digest.Write([]byte(path))
		digest.Write(data)
		return nil
	})
	if err != nil {
		return "", err
	}
	sum := digest.Sum(nil)
	version := fmt.Sprintf("1.%d.%d.%d", binary.BigEndian.Uint16(sum[0:2]), binary.BigEndian.Uint16(sum[2:4]), binary.BigEndian.Uint16(sum[4:6]))
	files["manifest.json"] = []byte(strings.Replace(string(files["manifest.json"]), `"version": "1.0.0"`, `"version": "`+version+`"`, 1))

	dir := fmt.Sprintf("%s-%x", e.extension, sum[:4])
	old, _ := filepath.Glob(e.extension + "*")
	for _, path := range old {
		if path != dir {
			removeExtension(path)
		}
	}
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return "", err
	}
	for name, data := range files {
		if err := os.WriteFile(filepath.Join(dir, name), data, 0o600); err != nil {
			return "", err
		}
	}
	config, _ := json.Marshal(map[string]string{"url": address})
	return dir, os.WriteFile(filepath.Join(dir, "config.json"), config, 0o600)
}

// removeExtension removes an old extension folder, which holds files only.
// os.RemoveAll can fail on Windows ("too many levels of symbolic links") where
// removing the files one by one works.
func removeExtension(dir string) {
	if os.RemoveAll(dir) == nil {
		return
	}
	entries, _ := os.ReadDir(dir)
	for _, entry := range entries {
		_ = os.Remove(filepath.Join(dir, entry.Name()))
	}
	_ = os.Remove(dir)
}

var upgrader = websocket.Upgrader{
	// Only the extension connects: it proves itself with the token in the
	// path, and its origin is chrome-extension://.
	CheckOrigin: func(r *http.Request) bool {
		return strings.HasPrefix(r.Header.Get("Origin"), "chrome-extension://")
	},
}

// serveRelay accepts the extension's WebSocket.
func (e *Engine) serveRelay(w http.ResponseWriter, r *http.Request) {
	e.mu.Lock()
	token := e.token
	e.mu.Unlock()
	if token == "" || r.URL.Path != "/ryotunes/"+token {
		http.NotFound(w, r)
		return
	}
	conn, err := upgrader.Upgrade(w, r, nil)
	if err != nil {
		return
	}
	conn.SetReadLimit(16 << 20)
	e.mu.Lock()
	old := e.page
	e.page = conn
	e.mu.Unlock()
	if old != nil {
		old.Close()
	}
	for {
		var message struct {
			Type string `json:"type"`
			JSON string `json:"json"`
		}
		if err := conn.ReadJSON(&message); err != nil {
			break
		}
		switch message.Type {
		case "hello":
			e.newPage()
		case "page":
			e.message(message.JSON)
		}
	}
	e.mu.Lock()
	if e.page == conn {
		e.page = nil
	}
	e.mu.Unlock()
}

// newPage resets what the engine knows when the player page (re)loads.
func (e *Engine) newPage() {
	e.mu.Lock()
	if e.readyDone {
		e.ready, e.readyDone = make(chan struct{}), false
	}
	e.state = State{}
	e.mu.Unlock()
}

func (e *Engine) browserExited(cmd *exec.Cmd) {
	e.mu.Lock()
	if e.cmd != cmd {
		e.mu.Unlock()
		return
	}
	e.cmd, e.state = nil, State{}
	if e.server != nil {
		e.server.Close()
		e.server = nil
	}
	e.token = ""
	if e.page != nil {
		e.page.Close()
		e.page = nil
	}
	if !e.readyDone {
		close(e.ready)
		e.readyDone = true
	}
	for id, ch := range e.pending {
		ch <- callResult{err: errors.New("Spotify's Web Player closed.")}
		delete(e.pending, id)
	}
	e.mu.Unlock()
	e.log.Info("Spotify Web Player closed")
}

func (e *Engine) message(payload string) {
	var msg struct {
		Type  string          `json:"type"`
		State json.RawMessage `json:"state"`
		Error string          `json:"error"`
		ID    int64           `json:"id"`
		OK    bool            `json:"ok"`
		Value json.RawMessage `json:"value"`
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
	case "result":
		e.mu.Lock()
		ch := e.pending[msg.ID]
		delete(e.pending, msg.ID)
		e.mu.Unlock()
		if ch != nil {
			if msg.OK {
				ch <- callResult{value: msg.Value}
			} else {
				ch <- callResult{err: errors.New(msg.Error)}
			}
		}
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
	case "net":
		// Spotify refusing a song's playback licence makes its player skip
		// to other songs: worth a warning in the diagnostics.
		var net struct {
			URL    string `json:"url"`
			Status int    `json:"status"`
		}
		if json.Unmarshal([]byte(payload), &net) == nil && strings.Contains(net.URL, "audio/license") {
			if net.Status >= 400 {
				e.log.Warn("Spotify refused the playback licence", "status", net.Status)
			} else {
				e.log.Info("Spotify granted the playback licence", "status", net.Status)
			}
		}
	}
}

// send writes one message to the extension.
func (e *Engine) send(message any) error {
	e.mu.Lock()
	page := e.page
	e.mu.Unlock()
	if page == nil {
		return errors.New("Spotify's Web Player is not connected.")
	}
	e.writeMu.Lock()
	defer e.writeMu.Unlock()
	_ = page.SetWriteDeadline(time.Now().Add(5 * time.Second))
	return page.WriteJSON(message)
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
	if args == nil {
		args = map[string]any{}
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
	if err := e.send(map[string]any{"type": "call", "id": id, "method": method, "args": args}); err != nil {
		forget()
		return nil, err
	}
	select {
	case got := <-result:
		if got.err != nil {
			return nil, fmt.Errorf("Spotify's Web Player refused %s: %w", method, got.err)
		}
		return got.value, nil
	case <-ctx.Done():
		forget()
		return nil, ctx.Err()
	}
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
	cmd = e.cmd
	e.shown = true
	e.mu.Unlock()
	if cmd == nil {
		return errors.New("Spotify's Web Player is not running.")
	}
	if err := e.send(map[string]any{"type": "navigate", "url": loginURL}); err != nil {
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
	e.mu.Lock()
	exited := e.exited
	e.mu.Unlock()
	pid := cmd.Process.Pid
	for tick := 0; ; tick++ {
		delay := time.Second
		if tick < 40 {
			delay = 50 * time.Millisecond // the first two seconds: catch the window as it opens
		}
		select {
		case <-exited:
			return
		case <-time.After(delay):
		}
		e.mu.Lock()
		shown, signingIn := e.shown, e.windows > 0
		e.mu.Unlock()
		// While a sign-in window is open, nothing is parked: it would be
		// parked too. The player's window stays where it already is.
		if !shown && !signingIn {
			showBrowser(pid, false)
		}
	}
}

// Close shuts the browser down, letting it save the sign-in first.
func (e *Engine) Close() {
	e.mu.Lock()
	cmd, exited := e.cmd, e.exited
	e.mu.Unlock()
	if cmd == nil || cmd.Process == nil {
		return
	}
	closeBrowser(cmd.Process.Pid)
	select {
	case <-exited:
	case <-time.After(5 * time.Second):
		_ = cmd.Process.Kill()
	}
}

func randomToken() string {
	buf := make([]byte, 24)
	_, _ = rand.Read(buf)
	return hex.EncodeToString(buf)
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
