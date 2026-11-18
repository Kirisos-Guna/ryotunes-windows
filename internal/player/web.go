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
	if p.web == nil {
		return errors.New("Spotify playback is not available on this system.")
	}
	uri := strings.TrimPrefix(s.URL, WebScheme)
	// Silence mpv first: it must not keep playing the previous song.
	_ = p.call(ctx, func(m *mpv.Mpv) error { return m.CommandAsync(p.nextAsyncID(), []string{"stop"}) })

	p.web.mu.Lock()
	p.web.active, p.web.uri, p.web.seen, p.web.ended = true, uri, false, false
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
	if _, err := engine.Call(ctx, "volume.set", map[string]any{"level": volume}); err != nil {
		p.log.Debug("web player volume", "error", err)
	}
	args := map[string]any{"contextUri": uri}
	if startAt > 0 {
		args["positionMs"] = int64(startAt * 1000)
	}
	if _, err := engine.Call(ctx, "player.play", args); err != nil {
		p.deactivateWeb()
		return err
	}
	if paused {
		_, _ = engine.Call(ctx, "player.pause", nil)
	}
	return nil
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
	go p.webCall("player.pause", nil)
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

// onWebState turns a page state into player events.
func (p *Player) onWebState(state webplayer.State) {
	p.web.mu.Lock()
	if !p.web.active || state.Track == nil {
		p.web.mu.Unlock()
		return
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
		atEnd := ours && state.IsPaused && state.DurationMs > 0 && state.Position(time.Now())*1000 >= state.DurationMs-1500
		if !ours || atEnd {
			p.web.ended, ended = true, true
		}
	}
	seen := p.web.seen
	p.web.mu.Unlock()

	if ended {
		// Stop whatever the page moved on to: the queue chooses what plays next.
		go p.webCall("player.pause", nil)
		p.emit(Event{Kind: EndOfFile})
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
	for {
		select {
		case <-p.closed:
			return
		case <-time.After(time.Duration(p.cadence.Load())):
		}
		p.web.mu.Lock()
		active, seen, ended := p.web.active, p.web.seen, p.web.ended
		p.web.mu.Unlock()
		if !active || !seen || ended {
			continue
		}
		state := p.web.engine.State()
		if state.Track == nil || state.IsPaused {
			continue
		}
		position := state.Position(time.Now())
		p.position.Store(math.Float64bits(position))
		p.emit(Event{Kind: Position, Position: position})
		// A song that reached its end without a new page state.
		if state.DurationMs > 0 && position*1000 >= state.DurationMs+1500 {
			p.onWebState(webplayer.State{Track: state.Track, IsPaused: true, PositionMs: state.DurationMs, DurationMs: state.DurationMs})
		}
	}
}
