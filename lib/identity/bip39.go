package identity

import (
	"crypto/sha256"
	_ "embed"
	"fmt"
	"strings"

	"github.com/DrSmithFr/web-ide/lib/i18n"
)

//go:embed wordlist.txt
var wordlistRaw string

// Wordlist is the BIP39 English list: 2048 words, one per line. The browser reads the same
// list from the relay (web/bip39.txt), so a phrase validates the same way on both sides.
var Wordlist = strings.Fields(wordlistRaw)

// Word returns the word of an index (0..2047), or "" out of range.
func Word(i int) string {
	if i < 0 || i >= len(Wordlist) {
		return ""
	}
	return Wordlist[i]
}

// wordIndex returns the index of a word, or -1 when it is not in the list.
func wordIndex(w string) int {
	for i, x := range Wordlist {
		if x == w {
			return i
		}
	}
	return -1
}

// Generate makes a phrase from entropy: 16, 20, 24, 28 or 32 bytes give 12, 15, 18, 21 or
// 24 words. The checksum is the first bits of SHA-256 of the entropy, as BIP39 defines it.
func Generate(entropy []byte) (string, error) {
	if len(entropy) < 16 || len(entropy) > 32 || len(entropy)%4 != 0 {
		return "", i18n.Errorf("entropy must be 16, 20, 24, 28 or 32 bytes, got %d", len(entropy))
	}
	h := sha256.Sum256(entropy)
	bits := bitString(entropy) + fmt.Sprintf("%08b", h[0])[:len(entropy)*8/32]
	var words []string
	for i := 0; i+11 <= len(bits); i += 11 {
		words = append(words, Word(bitsIndex(bits[i:i+11])))
	}
	return strings.Join(words, " "), nil
}

// Validate checks a phrase: the right number of words, words of the list, and the checksum.
func Validate(phrase string) error {
	words := strings.Fields(phrase)
	if len(words) != 12 && len(words) != 15 && len(words) != 18 && len(words) != 21 && len(words) != 24 {
		return i18n.Errorf("a phrase is 12, 15, 18, 21 or 24 words, got %d", len(words))
	}
	cbits := len(words) * 11 / 32
	bits := ""
	for _, w := range words {
		i := wordIndex(w)
		if i < 0 {
			return i18n.Errorf("%q is not a BIP39 word", w)
		}
		bits += fmt.Sprintf("%011b", i)
	}
	entropy := make([]byte, (len(bits)-cbits)/8)
	for i := 0; i < len(entropy); i++ {
		entropy[i] = byte(bitsIndex(bits[i*8 : i*8+8]))
	}
	if fmt.Sprintf("%08b", sha256.Sum256(entropy)[0])[:cbits] != bits[len(bits)-cbits:] {
		return i18n.New("the checksum of the phrase does not match")
	}
	return nil
}

// bitString writes the bits of the bytes, most significant first.
func bitString(b []byte) string {
	var s strings.Builder
	for _, x := range b {
		s.WriteString(fmt.Sprintf("%08b", x))
	}
	return s.String()
}

func bitsIndex(s string) int {
	v := 0
	for _, c := range s {
		v = v<<1 | int(c-'0')
	}
	return v
}
