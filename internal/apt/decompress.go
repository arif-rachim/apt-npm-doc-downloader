package apt

import (
	"bytes"
	"compress/gzip"
	"fmt"
	"io"
	"os/exec"
	"strings"
)

// decompress expands a Packages index. gzip is handled in-process because it
// is in the standard library and is what Ubuntu and most third party
// repositories publish; the rarer encodings shell out to the matching CLI so
// the tool keeps zero external Go dependencies.
func decompress(name string, body []byte) ([]byte, error) {
	switch {
	case strings.HasSuffix(name, ".gz"):
		zr, err := gzip.NewReader(bytes.NewReader(body))
		if err != nil {
			return nil, err
		}
		defer zr.Close()
		return io.ReadAll(zr)
	case strings.HasSuffix(name, ".xz"):
		return external("xz", []string{"-dc"}, body)
	case strings.HasSuffix(name, ".bz2"):
		return external("bzip2", []string{"-dc"}, body)
	case strings.HasSuffix(name, ".zst"):
		return external("zstd", []string{"-dc"}, body)
	default:
		return body, nil
	}
}

func external(bin string, args []string, body []byte) ([]byte, error) {
	if _, err := exec.LookPath(bin); err != nil {
		return nil, fmt.Errorf("index needs %s to decompress but it is not installed: %w", bin, err)
	}
	cmd := exec.Command(bin, args...)
	cmd.Stdin = bytes.NewReader(body)
	var out, errb bytes.Buffer
	cmd.Stdout = &out
	cmd.Stderr = &errb
	if err := cmd.Run(); err != nil {
		return nil, fmt.Errorf("%s: %v: %s", bin, err, strings.TrimSpace(errb.String()))
	}
	return out.Bytes(), nil
}
