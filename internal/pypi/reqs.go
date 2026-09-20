package pypi

import (
	"bufio"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// Requirement is one line of a requirements file.
type Requirement struct {
	Name    string
	Version string // empty when the requirement is not pinned with ==
	Marker  string
	Hashes  []string
	Raw     string
	// URL is set for a PEP 508 direct reference ("name @ https://...").
	URL string
}

// Pinned reports whether the requirement names an exact version.
func (r Requirement) Pinned() bool { return r.Version != "" }

// ParseRequirements reads a requirements.txt, following -r includes. Lines
// whose environment marker does not apply to env are dropped.
func ParseRequirements(path string, env Environment) ([]Requirement, error) {
	return parseRequirements(path, env, map[string]bool{})
}

func parseRequirements(path string, env Environment, seen map[string]bool) ([]Requirement, error) {
	abs, err := filepath.Abs(path)
	if err != nil {
		return nil, err
	}
	if seen[abs] {
		return nil, nil
	}
	seen[abs] = true

	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()

	var out []Requirement
	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 0, 64*1024), 1024*1024)
	var joined string
	for sc.Scan() {
		line := sc.Text()
		if strings.HasSuffix(strings.TrimRight(line, " \t"), "\\") {
			joined += strings.TrimSuffix(strings.TrimRight(line, " \t"), "\\") + " "
			continue
		}
		full := strings.TrimSpace(joined + line)
		joined = ""
		if full == "" {
			continue
		}
		if i := strings.Index(full, " #"); i >= 0 {
			full = strings.TrimSpace(full[:i])
		}
		if strings.HasPrefix(full, "#") || full == "" {
			continue
		}
		if strings.HasPrefix(full, "-r ") || strings.HasPrefix(full, "--requirement ") {
			inc := strings.TrimSpace(strings.SplitN(full, " ", 2)[1])
			if !filepath.IsAbs(inc) {
				inc = filepath.Join(filepath.Dir(path), inc)
			}
			sub, err := parseRequirements(inc, env, seen)
			if err != nil {
				return nil, err
			}
			out = append(out, sub...)
			continue
		}
		if strings.HasPrefix(full, "-") {
			continue // index URLs, flags, editable installs
		}
		req, ok, err := parseRequirementLine(full, env)
		if err != nil {
			return nil, err
		}
		if ok {
			out = append(out, req)
		}
	}
	return out, sc.Err()
}

// directReference splits a PEP 508 direct reference. It returns the project
// name (empty for a bare URL) and the URL.
func directReference(text string) (name, url string, ok bool) {
	for _, scheme := range []string{"http://", "https://", "file:", "git+", "hg+", "svn+", "bzr+"} {
		if strings.HasPrefix(text, scheme) {
			return "", text, true
		}
	}
	i := strings.Index(text, "@")
	if i < 0 {
		return "", "", false
	}
	rest := strings.TrimSpace(text[i+1:])
	if !strings.Contains(rest, "://") && !strings.HasPrefix(rest, "file:") {
		return "", "", false
	}
	name = strings.TrimSpace(text[:i])
	if j := strings.Index(name, "["); j >= 0 {
		name = name[:j]
	}
	return strings.TrimSpace(name), rest, true
}

// pathBase is filepath.Base for a URL path, without pulling in net/url for
// something this small.
func pathBase(u string) string {
	if i := strings.IndexAny(u, "?#"); i >= 0 {
		u = u[:i]
	}
	if i := strings.LastIndex(u, "/"); i >= 0 {
		return u[i+1:]
	}
	return u
}

func parseRequirementLine(line string, env Environment) (Requirement, bool, error) {
	r := Requirement{Raw: line}

	// Split off --hash options.
	fields := strings.Fields(line)
	var spec []string
	for i := 0; i < len(fields); i++ {
		if strings.HasPrefix(fields[i], "--hash") {
			h := strings.TrimPrefix(fields[i], "--hash")
			h = strings.TrimPrefix(h, "=")
			if h == "" && i+1 < len(fields) {
				i++
				h = fields[i]
			}
			r.Hashes = append(r.Hashes, strings.TrimPrefix(h, "sha256:"))
			continue
		}
		spec = append(spec, fields[i])
	}
	text := strings.Join(spec, " ")

	if i := strings.Index(text, ";"); i >= 0 {
		r.Marker = strings.TrimSpace(text[i+1:])
		text = strings.TrimSpace(text[:i])
	}
	if r.Marker != "" && !EvalMarker(r.Marker, env) {
		return Requirement{}, false, nil
	}
	// A direct reference is "name @ url" or a bare URL. Matching on the
	// "http" prefix alone would wrongly catch ordinary packages whose name
	// starts with it, such as httpx, httpcore and httptools.
	if name, url, ok := directReference(text); ok {
		switch {
		case strings.HasPrefix(url, "http://"), strings.HasPrefix(url, "https://"):
			r.Name = name
			r.URL = url
			if r.Name == "" {
				r.Name = projectOf(pathBase(url))
			}
			return r, true, nil
		default:
			return Requirement{}, false, fmt.Errorf(
				"%q points at %s, which cannot be mirrored from an index; "+
					"build a wheel for it and host it somewhere reachable, or drop it from the input", line, url)
		}
	}
	// name[extras]==version
	name := text
	if i := strings.IndexAny(text, "=<>!~"); i >= 0 {
		name = strings.TrimSpace(text[:i])
		rest := strings.TrimSpace(text[i:])
		if strings.HasPrefix(rest, "==") && !strings.Contains(rest, ",") {
			v := strings.TrimSpace(strings.TrimPrefix(rest, "=="))
			if !strings.HasSuffix(v, "*") {
				r.Version = v
			}
		}
	}
	if i := strings.Index(name, "["); i >= 0 {
		name = name[:i]
	}
	name = strings.TrimSpace(name)
	if name == "" {
		return Requirement{}, false, nil
	}
	r.Name = name
	return r, true, nil
}
