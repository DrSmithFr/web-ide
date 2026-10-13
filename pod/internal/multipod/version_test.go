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

// TestIsTag: only a release tag can be installed by *Update this pod* (scripts/install.sh takes
// one); a dev build or a git describe with a hash never matches.
func TestIsTag(t *testing.T) {
	for _, v := range []string{"v1.2.3", "v0.1.0", "v10.20.30"} {
		if !IsTag(v) {
			t.Errorf("%q is a release tag", v)
		}
	}
	for _, v := range []string{"dev", "1.10.0", "v1.10.0-12-gabc1234", "v1.10", "v1.10.0-dirty", "v1.10.0+local"} {
		if IsTag(v) {
			t.Errorf("%q is not a release tag", v)
		}
	}
}
