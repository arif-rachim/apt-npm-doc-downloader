// Package npm mirrors packages referenced by an npm lockfile.
package npm

import (
	"encoding/json"
	"fmt"
	"path"
	"sort"
	"strings"
)

// Entry is one downloadable tarball from a lockfile.
type Entry struct {
	Name      string
	Version   string
	Resolved  string
	Integrity string
	Dev       bool
	Optional  bool
	OS        []string
	CPU       []string
}

// TarballPath is where the tarball lands inside the bundle.
func (e Entry) TarballPath() string {
	base := path.Base(e.Resolved)
	if !strings.HasSuffix(base, ".tgz") {
		short := e.Name
		if i := strings.LastIndex(short, "/"); i >= 0 {
			short = short[i+1:]
		}
		base = fmt.Sprintf("%s-%s.tgz", short, e.Version)
	}
	return path.Join("npm/tarballs", e.Name, base)
}

type lockFile struct {
	LockfileVersion int                    `json:"lockfileVersion"`
	Packages        map[string]lockPackage `json:"packages"`
	Dependencies    map[string]lockDepV1   `json:"dependencies"`
}

type lockPackage struct {
	Name        string   `json:"name"`
	Version     string   `json:"version"`
	Resolved    string   `json:"resolved"`
	Integrity   string   `json:"integrity"`
	Link        bool     `json:"link"`
	Dev         bool     `json:"dev"`
	DevOptional bool     `json:"devOptional"`
	Optional    bool     `json:"optional"`
	OS          []string `json:"os"`
	CPU         []string `json:"cpu"`
}

type lockDepV1 struct {
	Version      string               `json:"version"`
	Resolved     string               `json:"resolved"`
	Integrity    string               `json:"integrity"`
	Dev          bool                 `json:"dev"`
	Optional     bool                 `json:"optional"`
	Dependencies map[string]lockDepV1 `json:"dependencies"`
}

// ParseLock reads package-lock.json v1, v2 or v3.
//
// Not every entry describes something downloadable: the root entry (""),
// workspace links, and git dependencies have no registry tarball, so they are
// skipped. Only entries whose "resolved" is an http(s) URL are returned.
func ParseLock(data []byte) ([]Entry, error) {
	var lf lockFile
	if err := json.Unmarshal(data, &lf); err != nil {
		return nil, err
	}
	seen := map[string]Entry{}

	for p, pkg := range lf.Packages {
		if p == "" || pkg.Link {
			continue
		}
		if !isRegistryURL(pkg.Resolved) {
			continue
		}
		name := pkg.Name
		if name == "" {
			name = nameFromPath(p)
		}
		if name == "" {
			continue
		}
		e := Entry{
			Name:      name,
			Version:   pkg.Version,
			Resolved:  pkg.Resolved,
			Integrity: pkg.Integrity,
			Dev:       pkg.Dev || pkg.DevOptional,
			Optional:  pkg.Optional,
			OS:        pkg.OS,
			CPU:       pkg.CPU,
		}
		key := e.Name + "@" + e.Version
		if old, ok := seen[key]; !ok || (old.Dev && !e.Dev) {
			seen[key] = e
		}
	}

	var walk func(map[string]lockDepV1)
	walk = func(deps map[string]lockDepV1) {
		for name, d := range deps {
			if isRegistryURL(d.Resolved) {
				e := Entry{Name: name, Version: d.Version, Resolved: d.Resolved, Integrity: d.Integrity, Dev: d.Dev, Optional: d.Optional}
				key := e.Name + "@" + e.Version
				if old, ok := seen[key]; !ok || (old.Dev && !e.Dev) {
					seen[key] = e
				}
			}
			if len(d.Dependencies) > 0 {
				walk(d.Dependencies)
			}
		}
	}
	walk(lf.Dependencies)

	out := make([]Entry, 0, len(seen))
	for _, e := range seen {
		out = append(out, e)
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Name != out[j].Name {
			return out[i].Name < out[j].Name
		}
		return out[i].Version < out[j].Version
	})
	return out, nil
}

func isRegistryURL(s string) bool {
	return strings.HasPrefix(s, "https://") || strings.HasPrefix(s, "http://")
}

// nameFromPath turns "node_modules/a/node_modules/@scope/b" into "@scope/b".
func nameFromPath(p string) string {
	const marker = "node_modules/"
	i := strings.LastIndex(p, marker)
	if i < 0 {
		return ""
	}
	return p[i+len(marker):]
}

// Keep reports whether an entry passes the dev/platform filters.
func Keep(e Entry, includeDev, onlyLinuxX64 bool) bool {
	if e.Dev && !includeDev {
		return false
	}
	if onlyLinuxX64 {
		if len(e.OS) > 0 && !matchesList(e.OS, "linux") {
			return false
		}
		if len(e.CPU) > 0 && !matchesList(e.CPU, "x64") {
			return false
		}
	}
	return true
}

// matchesList honours npm's negation syntax ("!win32").
func matchesList(list []string, want string) bool {
	positive := false
	anyPositive := false
	for _, v := range list {
		if strings.HasPrefix(v, "!") {
			if v[1:] == want {
				return false
			}
			continue
		}
		anyPositive = true
		if v == want || v == "any" {
			positive = true
		}
	}
	return positive || !anyPositive
}
