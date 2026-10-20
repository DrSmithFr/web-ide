package runtime

import "testing"

func TestTextFormats(t *testing.T) {
	cases := []struct {
		name string
		data string
		text string
		f    Format
	}{
		{"utf-8", "a\nb\n", "a\nb\n", Format{EncUTF8, EOLLF}},
		{"crlf", "a\r\nb\r\n", "a\nb\n", Format{EncUTF8, EOLCRLF}},
		{"bom", "\xef\xbb\xbfé\n", "é\n", Format{EncUTF8BOM, EOLLF}},
		{"utf-16le", "\xff\xfea\x00\r\x00\n\x00", "a\n", Format{EncUTF16LE, EOLCRLF}},
		{"utf-16be", "\xfe\xff\x00a\x00\n", "a\n", Format{EncUTF16BE, EOLLF}},
		{"latin", "caf\xe9\n", "café\n", Format{EncLatin, EOLLF}},
	}
	for _, c := range cases {
		text, f, ok := DecodeText([]byte(c.data))
		if !ok || text != c.text || f != c.f {
			t.Errorf("%s: decoded %q %+v %v", c.name, text, f, ok)
			continue
		}
		back, err := EncodeText(text, f)
		if err != nil || string(back) != c.data {
			t.Errorf("%s: encoded back %q %v", c.name, back, err)
		}
	}
	if _, _, ok := DecodeText([]byte("\x00\x01\x02binary")); ok {
		t.Error("binary data decoded as text")
	}
}
