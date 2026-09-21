package airgapkit

import (
	"bytes"
	"io/fs"
	"testing"
)

// The scripts run under bash on Ubuntu. A CRLF checkout on Windows would
// embed "\r" into every line and bash would fail with "bad interpreter" or
// "command not found", and only on the air-gapped side. .gitattributes pins
// them to LF; this catches a build made from a tree where that did not hold.
func TestEmbeddedScriptsUseLF(t *testing.T) {
	err := fs.WalkDir(scriptFS, "scripts", func(p string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return err
		}
		data, rerr := scriptFS.ReadFile(p)
		if rerr != nil {
			return rerr
		}
		if bytes.Contains(data, []byte("\r")) {
			t.Errorf("%s contains CR; scripts must use LF line endings", p)
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
}
