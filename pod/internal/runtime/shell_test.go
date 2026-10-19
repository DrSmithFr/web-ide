package runtime

import (
	"bytes"
	"strings"
	"testing"
)

func TestCappedOutput(t *testing.T) {
	c := &capped{}
	big := bytes.Repeat([]byte("x"), execHead+execTail+1000)
	if n, _ := c.Write(big); n != len(big) {
		t.Fatalf("write returned %d", n)
	}
	_, _ = c.Write([]byte("FIN"))
	text, cut := c.String()
	if !cut || !strings.HasSuffix(text, "FIN") || !strings.Contains(text, "output cut") || len(text) > execHead+execTail+100 {
		t.Fatalf("capped: cut=%v len=%d", cut, len(text))
	}
}
