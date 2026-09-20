package config

import (
	"encoding/json"
	"fmt"
	"os"
	"strings"
)

// AddSourcesToFile appends repositories to the "apt.sources" array of a
// configuration file, creating the file when it does not exist yet. The rest
// of the document is read back as generic JSON and written out unchanged, so
// hand written settings and comments-by-convention keys survive.
func AddSourcesToFile(path string, srcs []AptSource) error {
	if len(srcs) == 0 {
		return nil
	}
	doc := map[string]any{}
	if b, err := os.ReadFile(path); err == nil {
		if err := json.Unmarshal(b, &doc); err != nil {
			return fmt.Errorf("%s: %w", path, err)
		}
	} else if !os.IsNotExist(err) {
		return err
	}

	aptNode, _ := doc["apt"].(map[string]any)
	if aptNode == nil {
		aptNode = map[string]any{}
	}
	existing, _ := aptNode["sources"].([]any)

	have := map[string]bool{}
	for _, e := range existing {
		m, ok := e.(map[string]any)
		if !ok {
			continue
		}
		uri, _ := m["uri"].(string)
		have[strings.TrimRight(uri, "/")] = true
	}
	for _, s := range srcs {
		if have[strings.TrimRight(s.URI, "/")] {
			continue
		}
		entry := map[string]any{
			"name":       s.Name,
			"uri":        strings.TrimRight(s.URI, "/"),
			"suites":     s.Suites,
			"components": s.Components,
		}
		if s.Trusted {
			entry["trusted"] = true
		}
		if len(s.Arch) > 0 {
			entry["arch"] = s.Arch
		}
		existing = append(existing, entry)
	}
	aptNode["sources"] = existing
	doc["apt"] = aptNode

	out, err := json.MarshalIndent(doc, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(path, append(out, '\n'), 0o644)
}
