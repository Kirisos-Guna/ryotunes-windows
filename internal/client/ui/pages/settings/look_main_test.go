package settings

import (
	"os"
	"testing"
)

// The tests show the Ryoku desktop's look, as upstream does, unless a test
// chooses the look itself (Windows).
func TestMain(m *testing.M) {
	chooseLook = func() bool { return false }
	os.Exit(m.Run())
}
