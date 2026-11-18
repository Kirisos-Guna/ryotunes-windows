package spectrum

import (
	"math"
	"math/cmplx"
	"time"
)

// analyzer turns raw stereo samples into Bands spectrum levels in [0, 1],
// standing in for cava where the visualizer captures audio itself (the
// Windows loopback): a Hann-windowed FFT, log-spaced bands from 50 Hz,
// automatic sensitivity and cava-like fall-off.
type analyzer struct {
	ring   [fftSize]float32
	pos    int
	filled int
	last   time.Time
	window [fftSize]float64
	bins   [fftSize]complex128
	smooth [Bands]float32
	peakDB float64
}

const (
	fftSize       = 2048
	frameInterval = time.Second / 60
	lowHz         = 50.0
	dbRange       = 55.0
)

func newAnalyzer() *analyzer {
	a := &analyzer{peakDB: -30}
	for i := range a.window {
		a.window[i] = 0.5 - 0.5*math.Cos(2*math.Pi*float64(i)/float64(fftSize-1))
	}
	return a
}

// push adds interleaved stereo samples and, at most 60 times a second,
// returns new levels.
func (a *analyzer) push(stereo []float32, rate int, now time.Time) ([Bands]float32, bool) {
	for i := 0; i+1 < len(stereo); i += 2 {
		a.ring[a.pos] = (stereo[i] + stereo[i+1]) / 2
		a.pos = (a.pos + 1) % fftSize
		if a.filled < fftSize {
			a.filled++
		}
	}
	if a.filled < fftSize || now.Sub(a.last) < frameInterval || rate <= 0 {
		return [Bands]float32{}, false
	}
	a.last = now
	return a.levels(rate), true
}

func (a *analyzer) levels(rate int) [Bands]float32 {
	for i := 0; i < fftSize; i++ {
		a.bins[i] = complex(float64(a.ring[(a.pos+i)%fftSize])*a.window[i], 0)
	}
	fft(a.bins[:])

	highHz := math.Min(16000, float64(rate)/2)
	binHz := float64(rate) / fftSize
	var raw [Bands]float64
	frameMax := -200.0
	for band := 0; band < Bands; band++ {
		lo := lowHz * math.Pow(highHz/lowHz, float64(band)/Bands)
		hi := lowHz * math.Pow(highHz/lowHz, float64(band+1)/Bands)
		first := int(lo / binHz)
		last := max(first, int(hi/binHz))
		magnitude := 0.0
		for bin := first; bin <= last && bin < fftSize/2; bin++ {
			magnitude = math.Max(magnitude, cmplx.Abs(a.bins[bin]))
		}
		// Music has less energy up high; lift it as cava's equalizer does.
		centre := math.Sqrt(lo * hi)
		magnitude *= math.Pow(centre/1000, 0.35)
		db := 20 * math.Log10(magnitude/fftSize+1e-9)
		raw[band] = db
		frameMax = math.Max(frameMax, db)
	}
	// Automatic sensitivity: follow loud passages at once, quiet ones slowly.
	if frameMax > a.peakDB {
		a.peakDB = frameMax
	} else {
		a.peakDB = math.Max(frameMax, a.peakDB-0.15)
	}
	floor := a.peakDB - dbRange
	var out [Bands]float32
	for band := range raw {
		level := float32((raw[band] - floor) / dbRange)
		level = max(0, min(level, 1))
		level *= level // a quiet band stays low; peaks stand out
		// Rise at once, fall gently (cava's noise reduction).
		if level < a.smooth[band] {
			level = max(level, a.smooth[band]*0.86)
		}
		a.smooth[band] = level
		out[band] = level
	}
	return out
}

// fft is an in-place iterative radix-2 Cooley-Tukey transform.
func fft(x []complex128) {
	n := len(x)
	for i, j := 1, 0; i < n; i++ {
		bit := n >> 1
		for ; j&bit != 0; bit >>= 1 {
			j ^= bit
		}
		j ^= bit
		if i < j {
			x[i], x[j] = x[j], x[i]
		}
	}
	for size := 2; size <= n; size <<= 1 {
		step := cmplx.Rect(1, -2*math.Pi/float64(size))
		for start := 0; start < n; start += size {
			w := complex(1, 0)
			for k := 0; k < size/2; k++ {
				even, odd := x[start+k], x[start+k+size/2]*w
				x[start+k], x[start+k+size/2] = even+odd, even-odd
				w *= step
			}
		}
	}
}
