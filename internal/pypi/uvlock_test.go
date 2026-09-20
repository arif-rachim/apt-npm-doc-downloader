package pypi

import "testing"

func TestParseUVLock(t *testing.T) {
	pkgs, err := ParseUVLock("testdata/uv.lock")
	if err != nil {
		t.Fatal(err)
	}
	byName := map[string]LockPackage{}
	for _, p := range pkgs {
		byName[p.Name] = p
	}
	if len(pkgs) < 5 {
		t.Fatalf("expected several packages, got %d", len(pkgs))
	}

	cert, ok := byName["certifi"]
	if !ok {
		t.Fatal("certifi missing")
	}
	if cert.Source != "registry" || !cert.Downloadable() {
		t.Errorf("certifi source = %q", cert.Source)
	}
	if cert.Sdist.SHA256 == "" || cert.Sdist.Filename() == "" {
		t.Errorf("certifi sdist not parsed: %+v", cert.Sdist)
	}
	if len(cert.Wheels) == 0 || cert.Wheels[0].SHA256 == "" {
		t.Errorf("certifi wheels not parsed: %+v", cert.Wheels)
	}

	// The project itself is a virtual package with nothing to download.
	demo, ok := byName["demo"]
	if !ok {
		t.Fatal("virtual root package missing")
	}
	if demo.Downloadable() {
		t.Errorf("virtual package %q should not be downloadable", demo.Name)
	}

	// uv.lock is universal: it lists wheels for platforms we must filter out.
	cn, ok := byName["charset-normalizer"]
	if !ok {
		t.Fatal("charset-normalizer missing")
	}
	tg := target()
	compatible := 0
	for _, w := range cn.Wheels {
		wheel, ok := ParseWheelName(w.Filename())
		if !ok {
			t.Fatalf("cannot parse wheel name %q", w.Filename())
		}
		if _, ok := tg.Score(wheel); ok {
			compatible++
		}
	}
	if compatible == 0 || compatible == len(cn.Wheels) {
		t.Errorf("expected some but not all of %d wheels to match the target, got %d", len(cn.Wheels), compatible)
	}
}
