//go:build !cgo

// Pure-Go stub used for CGO-free builds (Ryotunes for Windows). Ryotunes
// decodes Spotify audio through libmpv, so this decoder is never reached.
package vorbis

import (
	"errors"
	"io"

	librespot "github.com/devgianlu/go-librespot"
)

const DataChunkSize = 4096

var errNoCgo = errors.New("vorbis: decoder unavailable in CGO-free build")

type Decoder struct {
	SampleRate int32
	Channels   int32
}

func New(log librespot.Logger, r librespot.SizedReadAtSeeker, meta *MetadataPage, gain float32) (*Decoder, error) {
	return nil, errNoCgo
}

func (d *Decoder) Close()                              {}
func (d *Decoder) Read(p []float32) (n int, err error) { return 0, errNoCgo }
func (d *Decoder) SetPositionMs(pos int64) error       { return errNoCgo }
func (d *Decoder) PositionMs() int64                   { return 0 }

type MetadataPage struct{}

func ExtractMetadataPage(log librespot.Logger, r io.ReaderAt, limit int64) (librespot.SizedReadAtSeeker, *MetadataPage, error) {
	return nil, nil, errNoCgo
}

func (m MetadataPage) GetSeekPosition(samplesPos int64) int64 { return 0 }
