package config

import (
	"os"
	"path/filepath"
	"testing"
)

func write(t *testing.T, name, content string) string {
	t.Helper()
	p := filepath.Join(t.TempDir(), name)
	if err := os.WriteFile(p, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
	return p
}

func TestParseOneLineSources(t *testing.T) {
	p := write(t, "ubuntu.list", `# comment
deb [arch=amd64 signed-by=/usr/share/keyrings/x.gpg] http://archive.ubuntu.com/ubuntu/ noble main universe
deb-src http://archive.ubuntu.com/ubuntu noble main
deb [trusted=yes] https://example.test/repo ./
`)
	got, err := ParseSourcesFile(p)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 2 {
		t.Fatalf("deb-src must be ignored; got %d entries: %+v", len(got), got)
	}
	if got[0].URI != "http://archive.ubuntu.com/ubuntu" || got[0].Suites[0] != "noble" {
		t.Errorf("unexpected first source: %+v", got[0])
	}
	if len(got[0].Arch) != 1 || got[0].Arch[0] != "amd64" {
		t.Errorf("arch option not parsed: %+v", got[0].Arch)
	}
	if len(got[0].Components) != 2 {
		t.Errorf("components not parsed: %+v", got[0].Components)
	}
	// A flat repository has a suite ending in "/" and no components.
	if got[1].Suites[0] != "./" || len(got[1].Components) != 0 || !got[1].Trusted {
		t.Errorf("flat repository not parsed: %+v", got[1])
	}
}

func TestParseDeb822Sources(t *testing.T) {
	p := write(t, "ubuntu.sources", `Types: deb deb-src
URIs: http://archive.ubuntu.com/ubuntu http://mirror.test/ubuntu
Suites: noble noble-updates
Components: main universe
Architectures: amd64
Signed-By: /usr/share/keyrings/ubuntu-archive-keyring.gpg

Types: deb
URIs: https://disabled.test/ubuntu
Suites: noble
Components: main
Enabled: no
`)
	got, err := ParseSourcesFile(p)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 2 {
		t.Fatalf("expected one stanza expanded into two URIs, got %d: %+v", len(got), got)
	}
	if got[0].URI != "http://archive.ubuntu.com/ubuntu" || len(got[0].Suites) != 2 {
		t.Errorf("unexpected source: %+v", got[0])
	}
	for _, s := range got {
		if s.URI == "https://disabled.test/ubuntu" {
			t.Error("a stanza with Enabled: no must be skipped")
		}
	}
}
