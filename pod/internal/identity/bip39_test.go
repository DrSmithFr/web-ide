package identity

import (
	"encoding/hex"
	"fmt"
	"strings"
	"testing"
)

// The official BIP39 English vectors (entropy → phrase): they check the wordlist and the
// checksum rule. The 24-word phrase of vectors.json is checked by the browser test, which
// must derive the same keys from it.
func TestBip39Vectors(t *testing.T) {
	vectors := map[string]string{
		"00000000000000000000000000000000":                                 "abandon abandon abandon abandon abandon abandon abandon abandon abandon abandon abandon about",
		"7f7f7f7f7f7f7f7f7f7f7f7f7f7f7f7f":                                 "legal winner thank year wave sausage worth useful legal winner thank yellow",
		"80808080808080808080808080808080":                                 "letter advice cage absurd amount doctor acoustic avoid letter advice cage above",
		"ffffffffffffffffffffffffffffffff":                                 "zoo zoo zoo zoo zoo zoo zoo zoo zoo zoo zoo wrong",
		"0000000000000000000000000000000000000000000000000000000000000000": "abandon abandon abandon abandon abandon abandon abandon abandon abandon abandon abandon abandon abandon abandon abandon abandon abandon abandon abandon abandon abandon abandon abandon art",
	}
	if len(Wordlist) != 2048 {
		t.Fatalf("wordlist: %d words", len(Wordlist))
	}
	for hexEnt, want := range vectors {
		e, err := hex.DecodeString(hexEnt)
		if err != nil {
			t.Fatal(err)
		}
		got, err := Generate(e)
		if err != nil {
			t.Fatalf("generate %s: %v", hexEnt, err)
		}
		if got != want {
			t.Errorf("generate %s:\n got %s\nwant %s", hexEnt, got, want)
		}
		if err := Validate(want); err != nil {
			t.Errorf("validate %q: %v", want, err)
		}
	}
}

func TestBip39Rejects(t *testing.T) {
	cases := map[string]string{
		"abandon abandon abandon abandon abandon abandon abandon abandon abandon abandon abandon zoo": "the checksum is wrong",
		"abandon ability able about above": "not a multiple of 11 bits",
		"":                                 "empty",
		"abandon abandon abandon abandon abandon abandon abandon abandon abandon abandon abandon zzz": "not a BIP39 word",
	}
	for phrase, why := range cases {
		err := Validate(phrase)
		if err == nil {
			t.Errorf("%q (%s) must be refused", phrase, why)
			continue
		}
		if why == "not a BIP39 word" && !strings.Contains(err.Error(), "BIP39 word") {
			t.Errorf("%q: unexpected error %v", phrase, err)
		}
		if why == "the checksum is wrong" && !strings.Contains(err.Error(), "checksum") {
			t.Errorf("%q: unexpected error %v", phrase, err)
		}
	}
}

// A phrase generated from entropy validates, and the same words give the same seed.
func TestGenerateRoundTrip(t *testing.T) {
	e := make([]byte, 32)
	for i := range e {
		e[i] = byte(i + 1)
	}
	p, err := Generate(e)
	if err != nil {
		t.Fatal(err)
	}
	if len(strings.Fields(p)) != 24 {
		t.Fatalf("24 words expected, got %d", len(strings.Fields(p)))
	}
	if err := Validate(p); err != nil {
		t.Fatalf("the generated phrase must validate: %v", err)
	}
	// The words of the phrase read back the original entropy.
	bits := ""
	for _, w := range strings.Fields(p) {
		bits += fmt.Sprintf("%011b", wordIndex(w))
	}
	if bits[:256] != bitString(e) {
		t.Error("the words must read back the entropy")
	}
}
