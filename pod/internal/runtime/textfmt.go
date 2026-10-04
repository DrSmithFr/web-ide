package runtime

import (
	"bytes"
	"strings"
	"unicode/utf8"

	"golang.org/x/text/encoding"
	"golang.org/x/text/encoding/charmap"
	"golang.org/x/text/encoding/unicode"

	"github.com/DrSmithFr/web-ide/pod/internal/i18n"
)

// Encodings of the text files. The editor always works on UTF-8 text with LF line ends:
// files are decoded on read and encoded back, in their own format, on write.
const (
	EncUTF8    = "utf-8"
	EncUTF8BOM = "utf-8-bom"
	EncUTF16LE = "utf-16le"
	EncUTF16BE = "utf-16be"
	EncLatin   = "windows-1252"
)

// Line separators.
const (
	EOLLF   = "lf"
	EOLCRLF = "crlf"
)

// Format is the encoding and line separator of a text file.
type Format struct {
	Encoding string `json:"encoding"`
	EOL      string `json:"eol"`
}

var (
	bomUTF8    = []byte{0xEF, 0xBB, 0xBF}
	bomUTF16LE = []byte{0xFF, 0xFE}
	bomUTF16BE = []byte{0xFE, 0xFF}
)

func codec(enc string) encoding.Encoding {
	switch enc {
	case EncUTF16LE:
		return unicode.UTF16(unicode.LittleEndian, unicode.IgnoreBOM)
	case EncUTF16BE:
		return unicode.UTF16(unicode.BigEndian, unicode.IgnoreBOM)
	case EncLatin:
		return charmap.Windows1252
	}
	return nil
}

// looksLatin tells a single-byte text (Windows-1252) from binary data: no NUL byte and
// almost no control characters besides tabs, line ends and form feeds.
func looksLatin(data []byte) bool {
	head := data[:min(len(data), 8000)]
	if bytes.IndexByte(head, 0) >= 0 {
		return false
	}
	ctrl := 0
	for _, b := range head {
		if b < 0x20 && b != '\t' && b != '\n' && b != '\r' && b != '\f' && b != 0x1b {
			ctrl++
		}
	}
	return ctrl*100 <= len(head)
}

// DecodeText decodes a file to UTF-8 with LF line ends; ok is false for binary data.
func DecodeText(data []byte) (text string, f Format, ok bool) {
	f.Encoding = EncUTF8
	switch {
	case bytes.HasPrefix(data, bomUTF8):
		f.Encoding, data = EncUTF8BOM, data[3:]
	case bytes.HasPrefix(data, bomUTF16LE):
		f.Encoding, data = EncUTF16LE, data[2:]
	case bytes.HasPrefix(data, bomUTF16BE):
		f.Encoding, data = EncUTF16BE, data[2:]
	}
	if c := codec(f.Encoding); c != nil {
		b, err := c.NewDecoder().Bytes(data)
		if err != nil {
			return "", f, false
		}
		text = string(b)
	} else if bytes.IndexByte(data[:min(len(data), 8000)], 0) < 0 && utf8.Valid(data) {
		text = string(data)
	} else if f.Encoding == EncUTF8 && looksLatin(data) {
		f.Encoding = EncLatin
		b, _ := charmap.Windows1252.NewDecoder().Bytes(data)
		text = string(b)
	} else {
		return "", f, false
	}
	// The most frequent separator wins; the file is written back with it only.
	crlf := strings.Count(text, "\r\n")
	f.EOL = EOLLF
	if crlf > 0 && crlf >= strings.Count(text, "\n")-crlf {
		f.EOL = EOLCRLF
	}
	if crlf > 0 {
		text = strings.ReplaceAll(text, "\r\n", "\n")
	}
	return text, f, true
}

// EncodeText encodes UTF-8 text with LF line ends in a file format.
func EncodeText(text string, f Format) ([]byte, error) {
	if f.EOL == EOLCRLF {
		text = strings.ReplaceAll(text, "\n", "\r\n")
	}
	switch f.Encoding {
	case "", EncUTF8:
		return []byte(text), nil
	case EncUTF8BOM:
		return append(append([]byte{}, bomUTF8...), text...), nil
	}
	c := codec(f.Encoding)
	if c == nil {
		return nil, i18n.Errorf("unknown encoding: %s", f.Encoding)
	}
	b, err := c.NewEncoder().Bytes([]byte(text))
	if err != nil {
		return nil, i18n.Errorf("the text has characters that %s cannot encode", f.Encoding)
	}
	switch f.Encoding {
	case EncUTF16LE:
		b = append(append([]byte{}, bomUTF16LE...), b...)
	case EncUTF16BE:
		b = append(append([]byte{}, bomUTF16BE...), b...)
	}
	return b, nil
}
