//go:build !windows

package spectrum

import "errors"

const loopbackSource = ""

func loopbackAvailable() bool { return false }

func startLoopback(func([]float32, int)) (*runningCapture, error) {
	return nil, errors.New("audio loopback is only used on Windows")
}
