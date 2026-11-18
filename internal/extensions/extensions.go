// Package extensions runs Myspoti extensions in Ryotunes.
//
// An extension is a folder with a manifest.json and a main.js, written for
// Myspoti's extension API (docs/extensions/API.md in the Myspoti repository).
// Ryotunes loads every folder in <config>/extensions and implements the part
// of the API that lyrics extensions use: myspoti.provide('lyrics', ...),
// myspoti.http (with the manifest's network hosts enforced), query,
// settings, storage, log/console and util. Each extension runs in its own
// JavaScript VM (goja) on its own goroutine, one call at a time.
//
// Hooks Ryotunes does not support yet (audio-source, download-source,
// blocks, pages, Spotify access...) are accepted and ignored, so an
// extension that also offers them still loads.
package extensions

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/ryoku-dev/ryotunes/internal/model"
	"github.com/ryoku-dev/ryotunes/internal/protocol"
)

// Manifest is the part of manifest.json Ryotunes reads.
type Manifest struct {
	ID          string    `json:"id"`
	Name        string    `json:"name"`
	Version     string    `json:"version"`
	Author      string    `json:"author"`
	Description string    `json:"description"`
	Actions     []Action  `json:"actions"`
	Settings    []Setting `json:"settings"`
	Main        string    `json:"main"`
}

// Action is one permission an extension asks for.
type Action struct {
	Name  string   `json:"name"`
	Hosts []string `json:"hosts"`
	Why   string   `json:"why"`
}

// Setting is one user setting with its default.
type Setting struct {
	Key     string          `json:"key"`
	Type    string          `json:"type"`
	Label   string          `json:"label"`
	Default json.RawMessage `json:"default"`
}

// Track is what a lyrics hook receives.
type Track struct {
	URI        string   `json:"uri"`
	ID         string   `json:"id"`
	Title      string   `json:"title"`
	Artist     string   `json:"artist"`
	Artists    []string `json:"artists"`
	Album      string   `json:"album,omitempty"`
	DurationMs int64    `json:"durationMs,omitempty"`
}

// Manager holds the loaded extensions.
type Manager struct {
	dir  string
	log  *slog.Logger
	mu   sync.RWMutex
	exts []*extension
}

// Dir is where extensions are installed: <config>/extensions.
func Dir(configDir string) string { return filepath.Join(configDir, "extensions") }

// Load reads every extension folder in dir. A folder that fails to load is
// logged and skipped.
func Load(dir string, log *slog.Logger) *Manager {
	if log == nil {
		log = slog.Default()
	}
	m := &Manager{dir: dir, log: log}
	_ = os.MkdirAll(dir, 0o700)
	entries, err := os.ReadDir(dir)
	if err != nil {
		return m
	}
	sort.Slice(entries, func(i, j int) bool { return entries[i].Name() < entries[j].Name() })
	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}
		ext, err := loadExtension(filepath.Join(dir, entry.Name()), log)
		if err != nil {
			log.Warn("extension not loaded", "folder", entry.Name(), "error", err)
			continue
		}
		log.Info("extension loaded", "id", ext.manifest.ID, "name", ext.manifest.Name, "version", ext.manifest.Version, "hooks", ext.hookNames())
		m.exts = append(m.exts, ext)
	}
	return m
}

// Installed lists the loaded extensions' manifests.
func (m *Manager) Installed() []Manifest {
	m.mu.RLock()
	defer m.mu.RUnlock()
	out := make([]Manifest, 0, len(m.exts))
	for _, ext := range m.exts {
		out = append(out, ext.manifest)
	}
	return out
}

// Lyrics asks each extension with a lyrics hook, in folder order. It returns
// the first synced answer, else the first plain one, else nil.
func (m *Manager) Lyrics(ctx context.Context, track Track) (*model.Lyrics, error) {
	if m == nil {
		return nil, nil
	}
	m.mu.RLock()
	exts := append([]*extension(nil), m.exts...)
	m.mu.RUnlock()
	var plain *model.Lyrics
	var errs []error
	for _, ext := range exts {
		if !ext.has("lyrics") {
			continue
		}
		callCtx, cancel := context.WithTimeout(ctx, lyricsBudget)
		value, err := ext.call(callCtx, "lyrics", track)
		cancel()
		if err != nil {
			errs = append(errs, fmt.Errorf("%s: %w", ext.manifest.Name, err))
			continue
		}
		lyrics, err := parseLyrics(value, ext.manifest.Name)
		if err != nil {
			errs = append(errs, fmt.Errorf("%s: %w", ext.manifest.Name, err))
			continue
		}
		if lyrics == nil {
			continue
		}
		if lyrics.Synced {
			return lyrics, nil
		}
		if plain == nil {
			plain = lyrics
		}
	}
	if plain != nil {
		return plain, nil
	}
	return nil, errors.Join(errs...)
}

// LyricsFor adapts Lyrics to the lyrics service's request.
func (m *Manager) LyricsFor(ctx context.Context, req protocol.LyricsParams) (*model.Lyrics, error) {
	if m == nil {
		return nil, nil
	}
	return m.Lyrics(ctx, TrackFromParams(req.VideoID, req.Title, req.Artists, req.Album, req.Duration))
}

// Close stops every extension.
func (m *Manager) Close() {
	if m == nil {
		return
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	for _, ext := range m.exts {
		ext.close()
	}
	m.exts = nil
}

const lyricsBudget = 8 * time.Second

// TrackFromParams builds a hook's track from a lyrics request.
func TrackFromParams(videoID, title, artists string, album *string, duration *float64) Track {
	track := Track{URI: videoID, Title: title}
	if _, id, ok := strings.Cut(videoID, "spotify:track:"); ok {
		track.ID = id
	} else {
		track.ID = videoID
	}
	for _, name := range strings.Split(artists, ",") {
		if name = strings.TrimSpace(name); name != "" {
			track.Artists = append(track.Artists, name)
		}
	}
	if len(track.Artists) > 0 {
		track.Artist = track.Artists[0]
	}
	if album != nil {
		track.Album = *album
	}
	if duration != nil && *duration > 0 {
		track.DurationMs = int64(*duration * 1000)
	}
	return track
}
