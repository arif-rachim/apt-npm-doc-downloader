package apt

import (
	"archive/tar"
	"bytes"
	"fmt"
	"io"
	"os"
	"strconv"
	"strings"
)

// ControlOf extracts the control stanza of a .deb file. A .deb is an ar
// archive holding control.tar.<compression>, which in turn holds ./control.
func ControlOf(path string) (string, error) {
	f, err := os.Open(path)
	if err != nil {
		return "", err
	}
	defer f.Close()

	magic := make([]byte, 8)
	if _, err := io.ReadFull(f, magic); err != nil {
		return "", err
	}
	if string(magic) != "!<arch>\n" {
		return "", fmt.Errorf("%s: not an ar archive", path)
	}
	hdr := make([]byte, 60)
	for {
		if _, err := io.ReadFull(f, hdr); err != nil {
			if err == io.EOF || err == io.ErrUnexpectedEOF {
				return "", fmt.Errorf("%s: no control member found", path)
			}
			return "", err
		}
		name := strings.TrimSpace(string(hdr[0:16]))
		name = strings.TrimSuffix(name, "/")
		size, err := strconv.ParseInt(strings.TrimSpace(string(hdr[48:58])), 10, 64)
		if err != nil {
			return "", fmt.Errorf("%s: bad ar header: %w", path, err)
		}
		if strings.HasPrefix(name, "control.tar") {
			body := make([]byte, size)
			if _, err := io.ReadFull(f, body); err != nil {
				return "", err
			}
			plain, err := decompress(name, body)
			if err != nil {
				return "", fmt.Errorf("%s: %w", path, err)
			}
			return controlFromTar(plain)
		}
		skip := size
		if skip%2 == 1 {
			skip++
		}
		if _, err := f.Seek(skip, io.SeekCurrent); err != nil {
			return "", err
		}
	}
}

func controlFromTar(b []byte) (string, error) {
	tr := tar.NewReader(bytes.NewReader(b))
	for {
		h, err := tr.Next()
		if err == io.EOF {
			return "", fmt.Errorf("control file not present in control.tar")
		}
		if err != nil {
			return "", err
		}
		name := strings.TrimPrefix(h.Name, "./")
		if name != "control" {
			continue
		}
		data, err := io.ReadAll(tr)
		if err != nil {
			return "", err
		}
		return strings.TrimRight(string(data), "\n"), nil
	}
}
