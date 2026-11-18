package state

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sync"

	"github.com/gofrs/flock"

	"github.com/ryoku-dev/ryotunes/internal/protocol"
)

// ErrClientLockHeld means another Ryotunes window process owns the client lock.
var ErrClientLockHeld = errors.New("Ryotunes client is already running")

// ClientLock holds the process-wide client singleton lock until Close.
type ClientLock struct {
	lock *flock.Flock
	once sync.Once
	err  error
}

// ClientLockPath returns the per-user client singleton lock path.
func ClientLockPath() string {
	return filepath.Join(protocol.RuntimeDir(), "client.lock")
}

// AcquireClientLock takes the client singleton lock without waiting.
func AcquireClientLock() (*ClientLock, error) {
	directory := protocol.RuntimeDir()
	if err := os.MkdirAll(directory, 0o700); err != nil {
		return nil, fmt.Errorf("create client runtime directory: %w", err)
	}
	if err := os.Chmod(directory, 0o700); err != nil {
		return nil, fmt.Errorf("secure client runtime directory: %w", err)
	}
	// gofrs/flock uses flock(2) on Unix and LockFileEx on Windows.
	lock := flock.New(ClientLockPath(), flock.SetPermissions(0o600))
	locked, err := lock.TryLock()
	if err != nil {
		return nil, fmt.Errorf("lock client: %w", err)
	}
	if !locked {
		return nil, ErrClientLockHeld
	}
	return &ClientLock{lock: lock}, nil
}

// Close releases the singleton lock. It is safe to call more than once.
func (lock *ClientLock) Close() error {
	if lock == nil {
		return nil
	}
	lock.once.Do(func() {
		lock.err = lock.lock.Close()
	})
	return lock.err
}
