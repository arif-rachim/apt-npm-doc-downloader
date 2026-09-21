package textio

import (
	"testing"
	"unicode/utf16"
)

func enc16(s string, big bool) []byte {
	out := []byte{0xFF, 0xFE}
	if big {
		out = []byte{0xFE, 0xFF}
	}
	for _, u := range utf16.Encode([]rune(s)) {
		if big {
			out = append(out, byte(u>>8), byte(u))
		} else {
			out = append(out, byte(u), byte(u>>8))
		}
	}
	return out
}

func TestDecode(t *testing.T) {
	const want = "requests==2.32.3\r\nflask # é\r\n"
	cases := map[string][]byte{
		"plain":   []byte(want),
		"utf8bom": append([]byte{0xEF, 0xBB, 0xBF}, want...),
		"utf16le": enc16(want, false),
		"utf16be": enc16(want, true),
	}
	for name, in := range cases {
		if got := string(Decode(in)); got != want {
			t.Errorf("%s: got %q want %q", name, got, want)
		}
	}
}
