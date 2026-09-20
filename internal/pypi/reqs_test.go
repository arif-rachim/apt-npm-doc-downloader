package pypi

import (
	"os"
	"path/filepath"
	"testing"
)

func writeReq(t *testing.T, content string) string {
	t.Helper()
	p := filepath.Join(t.TempDir(), "requirements.txt")
	if err := os.WriteFile(p, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
	return p
}

func TestPackagesNamedLikeAURLAreNotDirectReferences(t *testing.T) {
	// httpx, httpcore and httptools all begin with "http"; treating them as
	// URLs used to abort the whole run.
	p := writeReq(t, `httpx==0.27.2
httpcore==1.0.9
httptools==0.6.4
file-read-backwards==3.0.0
`)
	reqs, err := ParseRequirements(p, DefaultEnvironment(target()))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(reqs) != 4 {
		t.Fatalf("expected 4 requirements, got %d: %+v", len(reqs), reqs)
	}
	for _, r := range reqs {
		if r.URL != "" {
			t.Errorf("%s must not be read as a direct URL", r.Name)
		}
		if !r.Pinned() {
			t.Errorf("%s should be pinned", r.Name)
		}
	}
	if reqs[0].Name != "httpx" || reqs[0].Version != "0.27.2" {
		t.Errorf("unexpected first requirement: %+v", reqs[0])
	}
}

func TestDirectHTTPReferenceIsAccepted(t *testing.T) {
	p := writeReq(t, "mypkg @ https://example.test/wheels/mypkg-1.0-py3-none-any.whl\n")
	reqs, err := ParseRequirements(p, DefaultEnvironment(target()))
	if err != nil {
		t.Fatal(err)
	}
	if len(reqs) != 1 {
		t.Fatalf("expected 1 requirement, got %d", len(reqs))
	}
	if reqs[0].Name != "mypkg" || reqs[0].URL != "https://example.test/wheels/mypkg-1.0-py3-none-any.whl" {
		t.Errorf("unexpected direct reference: %+v", reqs[0])
	}
}

func TestUnmirrorableDirectReferenceIsRejected(t *testing.T) {
	p := writeReq(t, "mypkg @ git+https://github.test/org/mypkg.git@v1\n")
	if _, err := ParseRequirements(p, DefaultEnvironment(target())); err == nil {
		t.Error("a git reference cannot be mirrored and must be reported")
	}
}

func TestMarkersAndExtrasAndHashes(t *testing.T) {
	p := writeReq(t, `colorama==0.4.6 ; sys_platform == "win32"
uvicorn[standard]==0.30.6
certifi==2024.8.30 --hash=sha256:deadbeef
tomli==2.0.1 ; python_version < "3.11"
`)
	reqs, err := ParseRequirements(p, DefaultEnvironment(target()))
	if err != nil {
		t.Fatal(err)
	}
	names := map[string]Requirement{}
	for _, r := range reqs {
		names[r.Name] = r
	}
	if _, ok := names["colorama"]; ok {
		t.Error("a win32-only requirement must be dropped for the linux target")
	}
	if _, ok := names["tomli"]; ok {
		t.Error("a python_version < 3.11 requirement must be dropped for cp312")
	}
	if got := names["uvicorn"]; got.Version != "0.30.6" {
		t.Errorf("extras should be stripped from the name: %+v", got)
	}
	if got := names["certifi"]; len(got.Hashes) != 1 || got.Hashes[0] != "deadbeef" {
		t.Errorf("hash not parsed: %+v", got)
	}
}
