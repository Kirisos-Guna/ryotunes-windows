package spotify

import "testing"

func TestGroupDigits(t *testing.T) {
	for count, want := range map[uint64]string{7: "7", 999: "999", 1000: "1,000", 64382057: "64,382,057", 1435965: "1,435,965"} {
		if got := groupDigits(count); got != want {
			t.Errorf("groupDigits(%d) = %q, want %q", count, got, want)
		}
	}
}
