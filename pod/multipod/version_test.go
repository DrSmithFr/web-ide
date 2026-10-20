package multipod

import "testing"

// TestMismatch: the two words the home page shows, and nothing when the pods agree.
func TestMismatch(t *testing.T) {
	cases := []struct {
		own, other int
		want       string
	}{
		{Protocol, Protocol, ""},
		{Protocol, 0, MismatchOutdated}, // a pod older than #60 tells none
		{Protocol, Protocol + 1, MismatchNewer},
	}
	for _, c := range cases {
		if got := Mismatch(c.own, c.other); got != c.want {
			t.Errorf("Mismatch(%d, %d) = %q, want %q", c.own, c.other, got, c.want)
		}
	}
}
