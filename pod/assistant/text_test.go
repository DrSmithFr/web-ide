package assistant

import "testing"

func TestColorOutput(t *testing.T) {
	cases := []struct{ raw, want string }{
		{"plain\n", ""},
		{"\x1b[2Kno color\n", ""},
		{"\x1b]0;title\x07\x1b[31mred\x1b[0m \x1b[1A\x1b[1;32mok\x1b[m\n", "\x1b[31mred\x1b[0m \x1b[1;32mok\x1b[m\n"},
		{"\x1b[33m10%\r\x1b[33m100%\x1b[0m\r\n", "\x1b[33m100%\x1b[0m\n"},
	}
	for _, c := range cases {
		if got := ColorOutput(c.raw); got != c.want {
			t.Errorf("ColorOutput(%q) = %q, want %q", c.raw, got, c.want)
		}
		if plain := PlainOutput(c.raw); ColorOutput(c.raw) != "" && plain == ColorOutput(c.raw) {
			t.Errorf("PlainOutput(%q) kept colors", c.raw)
		}
	}
}
