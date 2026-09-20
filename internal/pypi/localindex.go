package pypi

import (
	"fmt"
	"html"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

// IndexFile is one artifact already stored in the bundle.
type IndexFile struct {
	Project  string
	Filename string
	SHA256   string
}

// GenerateSimpleIndex writes a PEP 503 index next to the downloaded files so
// the mirror is usable without Nexus:
//
//	pip install --index-url file:///srv/airgap-mirror/pypi/simple pkg
func GenerateSimpleIndex(bundleRoot string, files []IndexFile) error {
	byProject := map[string][]IndexFile{}
	for _, f := range files {
		p := Normalize(f.Project)
		byProject[p] = append(byProject[p], f)
	}
	root := filepath.Join(bundleRoot, "pypi", "simple")
	if err := os.MkdirAll(root, 0o755); err != nil {
		return err
	}

	projects := make([]string, 0, len(byProject))
	for p := range byProject {
		projects = append(projects, p)
	}
	sort.Strings(projects)

	var idx strings.Builder
	idx.WriteString("<!DOCTYPE html>\n<html><head><meta name=\"pypi:repository-version\" content=\"1.0\"><title>Simple index</title></head><body>\n")
	for _, p := range projects {
		fmt.Fprintf(&idx, "<a href=\"%s/\">%s</a><br/>\n", html.EscapeString(p), html.EscapeString(p))
	}
	idx.WriteString("</body></html>\n")
	if err := os.WriteFile(filepath.Join(root, "index.html"), []byte(idx.String()), 0o644); err != nil {
		return err
	}

	for _, p := range projects {
		entries := byProject[p]
		sort.Slice(entries, func(i, j int) bool { return entries[i].Filename < entries[j].Filename })
		dir := filepath.Join(root, p)
		if err := os.MkdirAll(dir, 0o755); err != nil {
			return err
		}
		var b strings.Builder
		fmt.Fprintf(&b, "<!DOCTYPE html>\n<html><head><meta name=\"pypi:repository-version\" content=\"1.0\"><title>Links for %s</title></head><body>\n<h1>Links for %s</h1>\n", html.EscapeString(p), html.EscapeString(p))
		seen := map[string]bool{}
		for _, f := range entries {
			if seen[f.Filename] {
				continue
			}
			seen[f.Filename] = true
			href := fmt.Sprintf("../../packages/%s/%s#sha256=%s", p, f.Filename, f.SHA256)
			fmt.Fprintf(&b, "<a href=\"%s\">%s</a><br/>\n", html.EscapeString(href), html.EscapeString(f.Filename))
		}
		b.WriteString("</body></html>\n")
		if err := os.WriteFile(filepath.Join(dir, "index.html"), []byte(b.String()), 0o644); err != nil {
			return err
		}
	}
	return nil
}
