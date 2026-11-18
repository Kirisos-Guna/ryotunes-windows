//go:build !cgo

// Package mp3: pure-Go stub used for CGO-free builds (Ryotunes for Windows).
// Ryotunes decodes Spotify audio through libmpv, so this decoder is never
// reached; it exists only so go-librespot's player package compiles.
package mp3

import (
	"errors"

	librespot "github.com/devgianlu/go-librespot"
)

var errNoCgo = errors.New("mp3: decoder unavailable in CGO-free build")

type Decoder struct {
	SampleRate int32
	Channels   int32
}

func New(log librespot.Logger, r librespot.SizedReadAtSeeker, gain float32) (*Decoder, error) {
	return nil, errNoCgo
}

func (d *Decoder) Read(p []float32) (n int, err error) { return 0, errNoCgo }
func (d *Decoder) SetPositionMs(pos int64) error       { return errNoCgo }
func (d *Decoder) PositionMs() int64                   { return 0 }
func (d *Decoder) Close() error                        { return nil }
