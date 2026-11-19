package player

import (
	"context"
	"errors"
	"math"
	"strings"
	"sync"
	"time"

	mpv "github.com/gen2brain/go-mpv"

	"github.com/ryoku-dev/ryotunes/internal/model"
	"github.com/ryoku-dev/ryotunes/internal/webplayer"
)

// WebScheme marks a stream the Spotify Web Player plays instead of mpv:
// "webplayer:spotify:track:<id>" (see internal/webplayer).
const WebScheme = "webplayer:"

// IsWebStream reports whether s plays in the Spotify Web Player.
func IsWebStream(s model.Stream) bool { return strings.HasPrefix(s.URL, WebScheme) }

// webSession is the player's side of the Web Player: while active, every
// transport call goes to the page and mpv stays stopped and unheard; the
// page's states become the same events mpv would send.
type webSession struct {
	engine *webplayer.Engine

	mu      sync.Mutex
	active  bool
	uri     string
	seen    bool // the page has started our track
	ended   bool // EndOfFile was sent for it
	volume  float64
	started time.Time
	// pausedAt is when the listener last paused (webPause).
	pausedAt time.Time
	lastPos  float64 // our song's last position while it played, seconds
	adSeen   bool    // an ad break ran since our song was requested
	// queued is the next song, put in Spotify's own queue (Prefetch): at
	// the end Spotify moves on to it itself, already loaded, and Ryotunes
	// follows (startWeb), instead of stopping the page and starting the
	// next song from nothing, which left 3 to 7 s of silence between songs.
	queued string
}

func (p *Player) webActive() bool {
	if p.web == nil {
		return false
	}
	p.web.mu.Lock()
	defer p.web.mu.Unlock()
	return p.web.active
}

func (p *Player) initWeb() {
	engine := webplayer.Default()
	if engine == nil {
		return
	}
	p.web = &webSession{engine: engine, volume: 1}
	engine.OnState(p.onWebState)
	go p.webTicker()
}

// loadWeb starts uri in the Web Player.
func (p *Player) loadWeb(ctx context.Context, s model.Stream, startAt float64, paused bool) error {
	err := p.startWeb(ctx, s, startAt, paused)
	if err != nil {
		p.webStopped()
	}
	return err
}

// webStopped records that nothing plays after a start that failed (sign-in
// needed, the browser could not start...): the player must not look as if
// it plays, or its clock would run past the song with nothing heard.
func (p *Player) webStopped() {
	if !p.paused.Swap(true) {
		p.emit(Event{Kind: PauseChanged, Paused: true})
	}
}

func (p *Player) startWeb(ctx context.Context, s model.Stream, startAt float64, paused bool) error {
	if p.web == nil {
		return errors.New("Spotify playback is not available on this system.")
	}
	uri := strings.TrimPrefix(s.URL, WebScheme)
	// Silence mpv first: it must not keep playing the previous song.
	_ = p.call(ctx, func(m *mpv.Mpv) error { return m.CommandAsync(p.nextAsyncID(), []string{"stop"}) })

	// The song Spotify was told to play next: it moves on to it by itself.
	p.web.mu.Lock()
	adopt := p.web.active && p.web.ended && p.web.queued == uri && !paused && startAt < 1
	p.web.mu.Unlock()
	if adopt {
		p.web.mu.Lock()
		p.web.uri, p.web.seen, p.web.ended, p.web.queued = uri, false, false, ""
		p.web.lastPos, p.web.adSeen = 0, false
		p.web.started = time.Now()
		p.web.mu.Unlock()
		p.position.Store(math.Float64bits(0))
		p.duration.Store(0)
		p.idle.Store(false)
		p.live.Store(false)
		p.paused.Store(false)
		go p.confirmQueuedStart(uri)
		return nil
	}
	if !paused {
		p.webForgetQueued(ctx)
	}

	p.web.mu.Lock()
	p.web.active, p.web.uri, p.web.seen, p.web.ended = true, uri, false, false
	p.web.lastPos, p.web.adSeen = 0, false
	p.web.started = time.Now()
	volume := p.web.volume
	p.web.mu.Unlock()
	if math.IsNaN(startAt) || math.IsInf(startAt, 0) || startAt < 0 {
		startAt = 0
	}
	p.position.Store(math.Float64bits(startAt))
	p.duration.Store(0)
	p.idle.Store(false)
	p.live.Store(false)
	p.paused.Store(paused)
	if paused {
		// Restoring a paused song (Ryotunes reopening) needs no browser:
		// play loads it then, at this position (webResume -> webReload).
		// Loading it now would also race a play pressed meanwhile.
		return nil
	}

	engine := p.web.engine
	signedIn, err := engine.SignedIn(ctx)
	if err != nil {
		p.deactivateWeb()
		return err
	}
	if !signedIn {
		p.deactivateWeb()
		if err := engine.ShowSignIn(ctx); err != nil {
			return err
		}
		return webplayer.ErrSignInRequired
	}
	// Always say where to start, 0 included: without it Spotify resumes the
	// song where this account last stopped it, not from the beginning.
	args := map[string]any{"contextUri": uri, "positionMs": int64(startAt * 1000)}
	if _, err := engine.Call(ctx, "player.play", args); err != nil {
		p.deactivateWeb()
		return err
	}
	p.setWebVolume(volume)
	go func() {
		// It waits a moment for an ad break to show; the song need not.
		ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
		defer cancel()
		p.resumeStalledAd(ctx)
	}()
	go p.confirmWebStart(uri, args)
	return nil
}

// confirmWebStart plays the song again if the page ends up paused on it
// right after it was asked to play: a page that has just loaded restores the
// account's last session (that song, paused where it stopped) a moment
// later, over Ryotunes' request, and the first song did not start.
func (p *Player) confirmWebStart(uri string, args map[string]any) {
	time.Sleep(1500 * time.Millisecond)
	p.web.mu.Lock()
	started, pausedByListener := p.web.started, p.web.pausedAt.After(p.web.started)
	current := p.web.active && p.web.uri == uri
	p.web.mu.Unlock()
	state := p.web.engine.State()
	if !current || pausedByListener || time.Since(started) > 5*time.Second || isAd(state) ||
		state.Track == nil || state.Track.URI != uri || !state.IsPaused {
		return
	}
	p.log.Info("Spotify's page restored its own session over the song; playing it again")
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	if _, err := p.web.engine.Call(ctx, "player.play", args); err != nil {
		p.log.Warn("Spotify Web Player command failed", "method", "player.play", "error", err)
	}
}

// setWebVolume sets the page's volume once it plays. Spotify answers a
// volume change only while it has an active device: asked before the first
// song of a session, it never answered, and the song waited 30 seconds for
// that call to time out.
func (p *Player) setWebVolume(level float64) {
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		if _, err := p.web.engine.Call(ctx, "volume.set", map[string]any{"level": level}); err != nil {
			p.log.Debug("web player volume", "error", err)
		}
	}()
}

func (p *Player) deactivateWeb() {
	if p.web == nil {
		return
	}
	p.web.mu.Lock()
	p.web.active = false
	p.web.mu.Unlock()
}

// leaveWeb pauses the page when mpv takes over again.
func (p *Player) leaveWeb() {
	if !p.webActive() {
		return
	}
	p.deactivateWeb()
	go func() {
		_ = p.webCall("player.pause", nil)
		ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
		defer cancel()
		p.webForgetQueued(ctx)
	}()
}

// confirmQueuedStart plays the queued song itself if Spotify did not move on
// to it (it stopped at the end, or played something else).
func (p *Player) confirmQueuedStart(uri string) {
	time.Sleep(5 * time.Second)
	p.web.mu.Lock()
	current, seen := p.web.active && p.web.uri == uri, p.web.seen
	p.web.mu.Unlock()
	state := p.web.engine.State()
	if !current || seen || isAd(state) {
		return
	}
	p.log.Info("Spotify did not move on to the queued song; starting it")
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	if _, err := p.web.engine.Call(ctx, "player.play", map[string]any{"contextUri": uri, "positionMs": 0}); err != nil {
		p.log.Warn("Spotify Web Player command failed", "method", "player.play", "error", err)
	}
}

// webOnTrack reports whether the page is on our track, or in an ad break
// before or after it: false after the browser restarted or the page
// reloaded, when it plays nothing.
func (p *Player) webOnTrack() bool {
	if !p.web.engine.Running() {
		return false
	}
	state := p.web.engine.State()
	if isAd(state) {
		return true
	}
	p.web.mu.Lock()
	defer p.web.mu.Unlock()
	return state.Track != nil && state.Track.URI == p.web.uri
}

// isAd reports Spotify playing an ad (a free account): part of playback,
// neither our song ending nor the page losing it.
func isAd(state webplayer.State) bool {
	return state.Track != nil && state.Track.Kind == "ad"
}

// webReload plays our track again from the current position, for a page
// that lost it, resuming or staying paused.
func (p *Player) webReload(resume bool) error {
	p.web.mu.Lock()
	uri := p.web.uri
	p.web.seen, p.web.ended = false, false
	volume := p.web.volume
	p.web.mu.Unlock()
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	engine := p.web.engine
	signedIn, err := engine.SignedIn(ctx)
	if err != nil {
		return err
	}
	if !signedIn {
		if err := engine.ShowSignIn(ctx); err != nil {
			return err
		}
		return webplayer.ErrSignInRequired
	}
	args := map[string]any{"contextUri": uri, "positionMs": int64(max(0, p.Position()) * 1000)}
	if _, err := engine.Call(ctx, "player.play", args); err != nil {
		return err
	}
	p.setWebVolume(volume)
	if !resume {
		_, err = engine.Call(ctx, "player.pause", nil)
		return err
	}
	p.resumeStalledAd(ctx)
	return nil
}

// resumeStalledAd un-pauses an ad break left paused: a new play request
// does not resume it, and the song would wait behind it for ever.
func (p *Player) resumeStalledAd(ctx context.Context) {
	time.Sleep(400 * time.Millisecond)
	if state := p.web.engine.State(); isAd(state) && state.IsPaused {
		_, _ = p.web.engine.Call(ctx, "player.resume", nil)
	}
}

// webResume resumes our track, reloading it when the page lost it.
func (p *Player) webResume() error {
	if !p.webOnTrack() {
		err := p.webReload(true)
		if err != nil {
			p.webStopped()
		}
		return err
	}
	return p.webCall("player.resume", nil)
}

// webPause pauses; a page without our track plays nothing to pause.
func (p *Player) webPause() error {
	p.web.mu.Lock()
	p.web.pausedAt = time.Now()
	p.web.mu.Unlock()
	if !p.webOnTrack() {
		if !p.paused.Swap(true) {
			p.emit(Event{Kind: PauseChanged, Paused: true})
		}
		return nil
	}
	return p.webCall("player.pause", nil)
}

// webToggle follows the page's own pause state, not a possibly stale one.
func (p *Player) webToggle() error {
	if !p.webOnTrack() || p.web.engine.State().IsPaused {
		return p.webResume()
	}
	return p.webPause()
}

// webSeek moves within our track, reloading it there when the page lost it.
func (p *Player) webSeek(pos float64) error {
	if isAd(p.web.engine.State()) {
		return nil // an ad cannot be skipped through; the song starts after it
	}
	p.position.Store(math.Float64bits(pos))
	if !p.webOnTrack() {
		return p.webReload(!p.Paused())
	}
	return p.webCall("player.seek", map[string]any{"positionMs": int64(pos * 1000)})
}

// webCall runs a bridge command for a transport control.
func (p *Player) webCall(method string, args any) error {
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	_, err := p.web.engine.Call(ctx, method, args)
	if err != nil {
		p.log.Warn("Spotify Web Player command failed", "method", method, "error", err)
	}
	return err
}

// webEnd ends our song once: the queue chooses what plays next, so the page
// is paused before Spotify moves on (autoplay) or rewinds to 0:00.
func (p *Player) webEnd() {
	p.web.mu.Lock()
	if p.web.ended || !p.web.active {
		p.web.mu.Unlock()
		return
	}
	p.web.ended = true
	continues := p.web.queued != ""
	p.web.mu.Unlock()
	if continues {
		// Skip to the queued song (already loaded) now: at the end of a
		// one-song context Spotify may instead rewind it, paused.
		go p.webCall("player.next", nil)
	} else {
		go p.webCall("player.pause", nil)
	}
	p.emit(Event{Kind: EndOfFile})
}

// webPrefetch puts the next song in Spotify's queue (see webSession.queued).
func (p *Player) webPrefetch(uri string) {
	p.web.mu.Lock()
	if !p.web.active || p.web.queued == uri || uri == p.web.uri {
		p.web.mu.Unlock()
		return
	}
	p.web.queued = uri
	p.web.mu.Unlock()
	go func() {
		// Spotify keeps its queue with the account, across sessions: songs
		// queued before (by Ryotunes or elsewhere) would play first.
		_ = p.webCall("queue.clear", nil)
		err := p.webCall("queue.add", map[string]any{"uris": []string{uri}})
		if err == nil {
			// Load it now (file, first audio, licence): Spotify does so only
			// 10 s before the end, too late after a seek near the end.
			_ = p.webCall("player.preloadNext", nil)
		}
		if err != nil {
			p.web.mu.Lock()
			if p.web.queued == uri {
				p.web.queued = ""
			}
			p.web.mu.Unlock()
		}
	}()
}

// webForgetQueued empties Spotify's queue of a song Ryotunes put there and
// no longer wants next: it would play after the song Ryotunes starts now.
func (p *Player) webForgetQueued(ctx context.Context) {
	p.web.mu.Lock()
	queued := p.web.queued
	p.web.queued = ""
	p.web.mu.Unlock()
	if queued != "" {
		_, _ = p.web.engine.Call(ctx, "queue.clear", nil)
	}
}

// onWebState turns a page state into player events.
func (p *Player) onWebState(state webplayer.State) {
	p.web.mu.Lock()
	if !p.web.active || state.Track == nil {
		p.web.mu.Unlock()
		return
	}
	if isAd(state) {
		// Spotify's ad break (a free account): wait for our song.
		p.web.adSeen = true
		p.web.mu.Unlock()
		if p.paused.Swap(state.IsPaused) != state.IsPaused {
			p.emit(Event{Kind: PauseChanged, Paused: state.IsPaused})
		}
		return
	}
	// A free account may get another song than the one asked for, after an
	// ad break or in its place. Follow what plays for our request, so
	// position and its end still work.
	requested := state.ContextURI == p.web.uri || p.web.adSeen
	if state.Track.URI != p.web.uri && !p.web.seen && requested && state.Track.Kind == "track" {
		p.log.Info("Spotify played another song after its ad break", "asked", p.web.uri, "playing", state.Track.URI)
		p.web.uri = state.Track.URI
	}
	ours := state.Track.URI == p.web.uri
	firstTime := ours && !p.web.seen
	if firstTime {
		p.web.seen = true
	}
	// The song is over when the page has moved on from it (Spotify's
	// autoplay or the context's next song), or stopped at its end.
	ended := false
	if p.web.seen && !p.web.ended {
		position := state.Position(time.Now())
		atEnd := ours && state.IsPaused && state.DurationMs > 0 && position*1000 >= state.DurationMs-1500
		// Spotify may rewind a finished single-song context to 0:00, paused.
		duration := state.DurationMs / 1000
		rewound := ours && state.IsPaused && duration > 0 && p.web.lastPos >= duration-4 && position < 3
		atEnd = atEnd || rewound
		// The page moved on from our song: its end only if the song was
		// almost over. Otherwise the page reloaded or was used elsewhere;
		// the next transport call plays our song again (webReload).
		movedOn := !ours && p.Duration() > 0 && p.Position() >= p.Duration()-15
		if atEnd || movedOn {
			ended = true
		} else if !ours {
			p.web.seen = false
		}
	}
	if ours && !state.IsPaused {
		p.web.lastPos = state.Position(time.Now())
	}
	seen := p.web.seen
	p.web.mu.Unlock()

	if ended {
		p.webEnd()
		return
	}
	if !ours || !seen {
		return
	}
	if firstTime {
		p.emit(Event{Kind: FileLoaded})
	}
	if duration := state.DurationMs / 1000; duration > 0 && duration != p.Duration() {
		p.duration.Store(math.Float64bits(duration))
		p.emit(Event{Kind: Duration, Duration: duration})
	}
	position := state.Position(time.Now())
	p.position.Store(math.Float64bits(position))
	if p.paused.Swap(state.IsPaused) != state.IsPaused {
		p.emit(Event{Kind: Position, Position: position})
		p.emit(Event{Kind: PauseChanged, Paused: state.IsPaused})
	}
}

// webTicker sends position events while the page plays, as mpv's time-pos
// observer does.
func (p *Player) webTicker() {
	// Checked often enough to end a song on time; positions are sent at
	// the player's cadence, as mpv's are.
	const check = 100 * time.Millisecond
	var lastEmit time.Time
	// The page's position can stall right at the end (Spotify reports
	// buffering while it prepares the next song): a song within a few
	// seconds of its end whose position has not moved for 2 s is over.
	var stalledAt float64
	var stalledSince time.Time
	for {
		select {
		case <-p.closed:
			return
		case <-time.After(check):
		}
		p.web.mu.Lock()
		active, seen, ended, uri := p.web.active, p.web.seen, p.web.ended, p.web.uri
		p.web.mu.Unlock()
		if !active || !seen || ended {
			continue
		}
		state := p.web.engine.State()
		if state.Track == nil || state.Track.URI != uri || state.IsPaused {
			continue
		}
		now := time.Now()
		position := state.Position(now)
		p.position.Store(math.Float64bits(position))
		p.web.mu.Lock()
		p.web.lastPos = position
		p.web.mu.Unlock()
		if now.Sub(lastEmit) >= time.Duration(p.cadence.Load()) {
			lastEmit = now
			p.emit(Event{Kind: Position, Position: position})
		}
		// End the song ourselves just before Spotify does, so it never
		// autoplays something else or rewinds to 0:00 first. Buffering at
		// the very end is Spotify preparing the next song: still the end.
		if state.DurationMs > 0 && position*1000 >= state.DurationMs-350 {
			p.webEnd()
			continue
		}
		if state.DurationMs > 0 && position*1000 >= state.DurationMs-4000 {
			if position != stalledAt {
				stalledAt, stalledSince = position, now
			} else if now.Sub(stalledSince) >= 2*time.Second {
				p.webEnd()
			}
		}
	}
}
