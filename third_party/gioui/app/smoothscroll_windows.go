// Ryotunes patch: smooth mouse-wheel scrolling on Windows.
//
// Gio sends each wheel notch (120 units) as one 120-pixel scroll, which
// jumps. A classic wheel (an external or Bluetooth mouse) sends whole
// notches; precision touchpads send small, already-smooth deltas. Notches
// are spread over a few frames with an ease-out instead, and a fast spin
// adds up; touchpad deltas pass straight through.

package app

import (
	"math"
	"syscall"

	"gioui.org/f32"
	"gioui.org/io/key"
	"gioui.org/io/pointer"

	"gioui.org/app/internal/windows"
)

const (
	smoothScrollTimer    = 0x52594f // an id of our own for SetTimer
	smoothScrollInterval = 8        // ms between steps (~120 Hz)
	smoothScrollShare    = 0.28     // of what is left, each step
	wheelNotch           = 120
)

var procKillTimer = syscall.NewLazyDLL("user32.dll").NewProc("KillTimer")

type smoothScroll struct {
	pending f32.Point
	pos     f32.Point
	buttons pointer.Buttons
	mods    key.Modifiers
	running bool
}

// isNotch reports a classic wheel's whole-notch delta.
func isNotch(delta float32) bool {
	return delta != 0 && math.Mod(math.Abs(float64(delta)), wheelNotch) == 0
}

// smoothScrollBy queues a notch's scroll and starts stepping it out.
func (w *window) smoothScrollBy(sp f32.Point, pos f32.Point, buttons pointer.Buttons, mods key.Modifiers) {
	s := &w.smooth
	// Reversing direction drops what was left going the other way.
	if sp.X*s.pending.X < 0 {
		s.pending.X = 0
	}
	if sp.Y*s.pending.Y < 0 {
		s.pending.Y = 0
	}
	s.pending = s.pending.Add(sp)
	s.pos, s.buttons, s.mods = pos, buttons, mods
	w.smoothScrollStep()
	if !s.running && (s.pending.X != 0 || s.pending.Y != 0) {
		if windows.SetTimer(w.hwnd, smoothScrollTimer, smoothScrollInterval, 0) == nil {
			s.running = true
		} else {
			w.emitScroll(s.pending) // no timer: scroll the rest at once
			s.pending = f32.Point{}
		}
	}
}

// smoothScrollStep sends the next share of the pending scroll.
func (w *window) smoothScrollStep() {
	s := &w.smooth
	step := f32.Point{X: stepOf(s.pending.X), Y: stepOf(s.pending.Y)}
	s.pending = s.pending.Sub(step)
	if step.X != 0 || step.Y != 0 {
		w.emitScroll(step)
	}
	if s.pending.X == 0 && s.pending.Y == 0 && s.running {
		procKillTimer.Call(uintptr(w.hwnd), smoothScrollTimer)
		s.running = false
	}
}

func stepOf(left float32) float32 {
	if math.Abs(float64(left)) <= 1 {
		return left // the remainder
	}
	step := left * smoothScrollShare
	if math.Abs(float64(step)) < 1 {
		step = float32(math.Copysign(1, float64(left)))
	}
	return step
}

func (w *window) emitScroll(sp f32.Point) {
	w.ProcessEvent(pointer.Event{
		Kind:      pointer.Scroll,
		Source:    pointer.Mouse,
		Position:  w.smooth.pos,
		Buttons:   w.smooth.buttons,
		Scroll:    sp,
		Modifiers: w.smooth.mods,
		Time:      windows.GetMessageTime(),
	})
}
