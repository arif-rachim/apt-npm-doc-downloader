package npm

import "testing"

const lockV3 = `{
  "name": "demo",
  "lockfileVersion": 3,
  "packages": {
    "": { "name": "demo", "version": "1.0.0", "dependencies": { "left-pad": "^1.0.0" } },
    "packages/shared": { "name": "@demo/shared", "version": "1.0.0" },
    "node_modules/@demo/shared": { "resolved": "packages/shared", "link": true },
    "node_modules/left-pad": {
      "version": "1.3.0",
      "resolved": "https://registry.npmjs.org/left-pad/-/left-pad-1.3.0.tgz",
      "integrity": "sha512-XI5MPzVNApjAyhQzphX8BkmKsKUxD4LdyK24iZeQGinBN9yTQT3bFlCBy/aVx2HrNcqQGsdot8ghrjyrvMCoEg=="
    },
    "node_modules/from-git": {
      "version": "2.0.0",
      "resolved": "git+ssh://git@github.com/org/repo.git#0123456789abcdef0123456789abcdef01234567"
    },
    "node_modules/typescript": {
      "version": "5.6.2",
      "dev": true,
      "resolved": "https://registry.npmjs.org/typescript/-/typescript-5.6.2.tgz",
      "integrity": "sha512-NW8ByodCSNCwZeghjN3o+JX5OFH0Ojg6sadjEKY4huZ52TqbJTJnDo5+Tw98lSy63NZvi4n+ez5m2u5d4PkZyg=="
    },
    "node_modules/fsevents": {
      "version": "2.3.3",
      "optional": true,
      "os": ["darwin"],
      "cpu": ["arm64", "x64"],
      "resolved": "https://registry.npmjs.org/fsevents/-/fsevents-2.3.3.tgz",
      "integrity": "sha512-5xoDfX+fL7faATnagmWPpbFtwh/R77WmMMqqHGS65C3vvB0YHrgF+B1YmZ3441tMj5n63k0212XNoJwzlhffQw=="
    },
    "node_modules/esbuild-linux": {
      "version": "0.21.5",
      "optional": true,
      "os": ["linux"],
      "cpu": ["x64"],
      "resolved": "https://registry.npmjs.org/esbuild-linux/-/esbuild-linux-0.21.5.tgz",
      "integrity": "sha512-1rYdTpyv03iycF1+BhzrzQJCdOuAOtaqHTWJZCWvijKD2N5Xu0TtVC8/+1faWqcP9iBCWOmjmhoH94dH82BxPQ=="
    }
  }
}`

func TestParseLockSkipsWhatCannotBeDownloaded(t *testing.T) {
	entries, err := ParseLock([]byte(lockV3))
	if err != nil {
		t.Fatal(err)
	}
	got := map[string]Entry{}
	for _, e := range entries {
		got[e.Name] = e
	}
	for _, unwanted := range []string{"demo", "@demo/shared", "from-git"} {
		if _, ok := got[unwanted]; ok {
			t.Errorf("%q has no registry tarball and must be skipped", unwanted)
		}
	}
	for _, wanted := range []string{"left-pad", "typescript", "fsevents", "esbuild-linux"} {
		if _, ok := got[wanted]; !ok {
			t.Errorf("%q should have been collected", wanted)
		}
	}
	if !got["typescript"].Dev {
		t.Error("typescript should be marked as a dev dependency")
	}
	if got["left-pad"].TarballPath() != "npm/tarballs/left-pad/left-pad-1.3.0.tgz" {
		t.Errorf("unexpected tarball path %q", got["left-pad"].TarballPath())
	}
}

func TestKeepFilters(t *testing.T) {
	entries, err := ParseLock([]byte(lockV3))
	if err != nil {
		t.Fatal(err)
	}
	byName := map[string]Entry{}
	for _, e := range entries {
		byName[e.Name] = e
	}
	if Keep(byName["typescript"], false, false) {
		t.Error("dev dependency must be dropped when include-dev is off")
	}
	if !Keep(byName["typescript"], true, false) {
		t.Error("dev dependency must be kept when include-dev is on")
	}
	if Keep(byName["fsevents"], true, true) {
		t.Error("darwin-only package must be dropped when filtering to linux/x64")
	}
	if !Keep(byName["esbuild-linux"], true, true) {
		t.Error("linux/x64 package must be kept when filtering to linux/x64")
	}
	if !Keep(byName["left-pad"], true, true) {
		t.Error("package without os/cpu constraints must always be kept")
	}
}

func TestScopedPackagePath(t *testing.T) {
	e := Entry{Name: "@types/node", Version: "20.14.0", Resolved: "https://registry.npmjs.org/@types/node/-/node-20.14.0.tgz"}
	if got := e.TarballPath(); got != "npm/tarballs/@types/node/node-20.14.0.tgz" {
		t.Errorf("unexpected scoped path %q", got)
	}
}
