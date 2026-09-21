// Package textio reads text files written by Windows tools.
//
// Notepad adds a UTF-8 byte order mark, and PowerShell 5.1 redirection
// (`>`) writes UTF-16. Neither is valid JSON or requirements syntax as is, so
// every user supplied input file goes through here first.
package textio

import (
	"bytes"
	"os"
	"unicode/utf16"
)

// Decode returns b as plain UTF-8: a leading UTF-8 BOM is dropped, and UTF-16
// (either byte order, recognised by its BOM) is converted.
func Decode(b []byte) []byte {
	switch {
	case bytes.HasPrefix(b, []byte{0xEF, 0xBB, 0xBF}):
		return b[3:]
	case bytes.HasPrefix(b, []byte{0xFF, 0xFE}):
		return utf16To8(b[2:], false)
	case bytes.HasPrefix(b, []byte{0xFE, 0xFF}):
		return utf16To8(b[2:], true)
	}
	return b
}

// ReadFile is os.ReadFile followed by Decode.
func ReadFile(path string) ([]byte, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	return Decode(b), nil
}

func utf16To8(b []byte, bigEndian bool) []byte {
	u := make([]uint16, 0, len(b)/2)
	for i := 0; i+1 < len(b); i += 2 {
		if bigEndian {
			u = append(u, uint16(b[i])<<8|uint16(b[i+1]))
		} else {
			u = append(u, uint16(b[i+1])<<8|uint16(b[i]))
		}
	}
	return []byte(string(utf16.Decode(u)))
}
