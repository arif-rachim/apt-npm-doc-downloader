package pypi

import (
	"os"
	"path"
	"regexp"
	"strings"
)

// LockPackage is one [[package]] stanza of a uv.lock.
type LockPackage struct {
	Name    string
	Version string
	Source  string // registry | virtual | editable | directory | git | path
	Sdist   LockArtifact
	Wheels  []LockArtifact
}

// LockArtifact is a downloadable file referenced by the lockfile.
type LockArtifact struct {
	URL    string
	SHA256 string
}

// Filename derives the artifact name from its URL; uv.lock does not store it.
func (a LockArtifact) Filename() string { return path.Base(a.URL) }

var (
	kvRE    = regexp.MustCompile(`^\s*([A-Za-z0-9_.-]+)\s*=\s*(.*)$`)
	urlRE   = regexp.MustCompile(`url\s*=\s*"([^"]+)"`)
	hashRE  = regexp.MustCompile(`hash\s*=\s*"([^"]+)"`)
	regRE   = regexp.MustCompile(`^\{\s*([a-z-]+)\s*=`)
	quoteRE = regexp.MustCompile(`^"(.*)"$`)
)

// ParseUVLock reads the subset of uv.lock that matters for mirroring.
//
// Two traps this handles: uv.lock stores no filename (it is derived from the
// URL), and the lockfile is universal, so it lists wheels for every platform
// and Python version. Callers must still pick a wheel with Target.Score.
func ParseUVLock(path string) ([]LockPackage, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var out []LockPackage
	var cur *LockPackage
	lines := strings.Split(strings.ReplaceAll(string(b), "\r\n", "\n"), "\n")

	flush := func() {
		if cur != nil && cur.Name != "" {
			out = append(out, *cur)
		}
		cur = nil
	}

	for i := 0; i < len(lines); i++ {
		line := lines[i]
		trimmed := strings.TrimSpace(line)
		if trimmed == "[[package]]" {
			flush()
			cur = &LockPackage{}
			continue
		}
		if strings.HasPrefix(trimmed, "[") && trimmed != "[[package]]" {
			// Any other table ends the package's top-level fields, but
			// sub-tables such as [package.metadata] carry no artifacts.
			if strings.HasPrefix(trimmed, "[[package.") || strings.HasPrefix(trimmed, "[package.") {
				continue
			}
			flush()
			continue
		}
		if cur == nil {
			continue
		}
		m := kvRE.FindStringSubmatch(line)
		if m == nil {
			continue
		}
		key, val := m[1], strings.TrimSpace(m[2])
		switch key {
		case "name":
			cur.Name = unquote(val)
		case "version":
			cur.Version = unquote(val)
		case "source":
			if g := regRE.FindStringSubmatch(val); g != nil {
				cur.Source = g[1]
			}
		case "sdist":
			block := val
			if !strings.Contains(block, "}") {
				block, i = gather(lines, i, "}")
			}
			cur.Sdist = artifactFrom(block)
		case "wheels":
			block := val
			if !strings.Contains(block, "]") {
				block, i = gather(lines, i, "]")
			}
			for _, chunk := range splitInlineTables(block) {
				if a := artifactFrom(chunk); a.URL != "" {
					cur.Wheels = append(cur.Wheels, a)
				}
			}
		}
	}
	flush()
	return out, nil
}

// gather joins lines until the one containing the closing token.
func gather(lines []string, start int, closing string) (string, int) {
	var b strings.Builder
	b.WriteString(lines[start])
	i := start
	for i+1 < len(lines) {
		i++
		b.WriteString(" ")
		b.WriteString(strings.TrimSpace(lines[i]))
		if strings.Contains(lines[i], closing) {
			break
		}
	}
	return b.String(), i
}

func splitInlineTables(s string) []string {
	var out []string
	depth := 0
	start := -1
	for i, c := range s {
		switch c {
		case '{':
			if depth == 0 {
				start = i
			}
			depth++
		case '}':
			depth--
			if depth == 0 && start >= 0 {
				out = append(out, s[start:i+1])
				start = -1
			}
		}
	}
	return out
}

func artifactFrom(s string) LockArtifact {
	a := LockArtifact{}
	if m := urlRE.FindStringSubmatch(s); m != nil {
		a.URL = m[1]
	}
	if m := hashRE.FindStringSubmatch(s); m != nil {
		a.SHA256 = strings.TrimPrefix(m[1], "sha256:")
	}
	return a
}

func unquote(s string) string {
	if m := quoteRE.FindStringSubmatch(strings.TrimSpace(s)); m != nil {
		return m[1]
	}
	return strings.TrimSpace(s)
}

// Downloadable reports whether the package has artifacts on an index.
// virtual/editable/directory/git sources have nothing to mirror.
func (p LockPackage) Downloadable() bool {
	switch p.Source {
	case "registry", "":
		return true
	default:
		return false
	}
}
