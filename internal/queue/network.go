package queue

import (
	"context"
	"errors"
	"net"
	"strings"
	"time"
)

// networkRetryDelays are the waits before each new try of a song that
// failed for want of a network: about half a minute in all.
var networkRetryDelays = []time.Duration{2 * time.Second, 4 * time.Second, 8 * time.Second, 8 * time.Second, 8 * time.Second}

// networkFailure reports a failure of the network rather than of the song:
// a dial, DNS, TLS or timeout error, a reset or refused connection, or
// Spotify's Web Player out of reach. Providers often wrap these into their
// own errors, so their words count too.
func networkFailure(err error) bool {
	if err == nil || errors.Is(err, context.Canceled) {
		return false
	}
	var netErr net.Error
	if errors.As(err, &netErr) || errors.Is(err, context.DeadlineExceeded) {
		return true
	}
	message := strings.ToLower(err.Error())
	for _, sign := range []string{
		"connection reset", "connection refused", "no such host", "network is unreachable",
		"i/o timeout", "tls handshake", "unexpected eof", "connectex",
		"couldn't reach", "didn't answer in time",
		"web player is not connected", "web player did not start",
	} {
		if strings.Contains(message, sign) {
			return true
		}
	}
	return false
}
