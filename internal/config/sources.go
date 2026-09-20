package config

import (
	"bufio"
	"fmt"
	"os"
	"strings"
)

// ParseSourcesFile reads either classic one-line sources.list syntax or the
// deb822 .sources format and returns the binary ("deb") entries.
func ParseSourcesFile(path string) ([]AptSource, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	text := string(b)
	if looksDeb822(text) {
		return parseDeb822(text, path)
	}
	return parseOneLine(text, path)
}

func looksDeb822(text string) bool {
	sc := bufio.NewScanner(strings.NewReader(text))
	for sc.Scan() {
		line := strings.TrimSpace(sc.Text())
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		return strings.HasPrefix(strings.ToLower(line), "types:")
	}
	return false
}

func parseOneLine(text, path string) ([]AptSource, error) {
	var out []AptSource
	sc := bufio.NewScanner(strings.NewReader(text))
	n := 0
	for sc.Scan() {
		n++
		line := strings.TrimSpace(sc.Text())
		if i := strings.Index(line, "#"); i >= 0 {
			line = strings.TrimSpace(line[:i])
		}
		if line == "" {
			continue
		}
		fields := strings.Fields(line)
		if fields[0] != "deb" {
			continue // deb-src is not useful for an offline binary mirror
		}
		fields = fields[1:]
		var arch []string
		var trusted bool
		for len(fields) > 0 && strings.HasPrefix(fields[0], "[") {
			// Options may be written as [a=b c=d] across several fields.
			var opts []string
			for len(fields) > 0 {
				f := fields[0]
				fields = fields[1:]
				opts = append(opts, strings.Trim(f, "[]"))
				if strings.HasSuffix(f, "]") {
					break
				}
			}
			for _, o := range opts {
				k, v, ok := strings.Cut(o, "=")
				if !ok {
					continue
				}
				switch strings.ToLower(k) {
				case "arch", "architectures":
					arch = splitList(v)
				case "trusted":
					trusted = strings.EqualFold(v, "yes") || strings.EqualFold(v, "true")
				}
			}
		}
		if len(fields) < 2 {
			return nil, fmt.Errorf("%s:%d: malformed deb line", path, n)
		}
		src := AptSource{
			URI:     strings.TrimRight(fields[0], "/"),
			Suites:  []string{fields[1]},
			Arch:    arch,
			Trusted: trusted,
		}
		if len(fields) > 2 {
			src.Components = fields[2:]
		}
		src.Name = sourceName(src)
		out = append(out, src)
	}
	return out, sc.Err()
}

func parseDeb822(text, path string) ([]AptSource, error) {
	var out []AptSource
	for _, stanza := range strings.Split(strings.ReplaceAll(text, "\r\n", "\n"), "\n\n") {
		fields := map[string]string{}
		var key string
		for _, line := range strings.Split(stanza, "\n") {
			if strings.TrimSpace(line) == "" || strings.HasPrefix(strings.TrimSpace(line), "#") {
				continue
			}
			if strings.HasPrefix(line, " ") || strings.HasPrefix(line, "\t") {
				if key != "" {
					fields[key] += " " + strings.TrimSpace(line)
				}
				continue
			}
			k, v, ok := strings.Cut(line, ":")
			if !ok {
				continue
			}
			key = strings.ToLower(strings.TrimSpace(k))
			fields[key] = strings.TrimSpace(v)
		}
		if fields["types"] == "" {
			continue
		}
		types := splitList(fields["types"])
		if !contains(types, "deb") {
			continue
		}
		enabled := fields["enabled"]
		if enabled != "" && !strings.EqualFold(enabled, "yes") && !strings.EqualFold(enabled, "true") {
			continue
		}
		for _, uri := range splitList(fields["uris"]) {
			src := AptSource{
				URI:        strings.TrimRight(uri, "/"),
				Suites:     splitList(fields["suites"]),
				Components: splitList(fields["components"]),
				Arch:       splitList(fields["architectures"]),
				Trusted:    strings.EqualFold(fields["trusted"], "yes"),
			}
			if len(src.Suites) == 0 {
				return nil, fmt.Errorf("%s: deb822 stanza without Suites", path)
			}
			src.Name = sourceName(src)
			out = append(out, src)
		}
	}
	return out, nil
}

func sourceName(s AptSource) string {
	host := s.URI
	host = strings.TrimPrefix(strings.TrimPrefix(host, "https://"), "http://")
	host = strings.ReplaceAll(host, "/", "-")
	return host + "-" + strings.Join(s.Suites, "-")
}

func splitList(s string) []string {
	f := strings.FieldsFunc(s, func(r rune) bool { return r == ',' || r == ' ' || r == '\t' })
	var out []string
	for _, v := range f {
		if v = strings.TrimSpace(v); v != "" {
			out = append(out, v)
		}
	}
	return out
}

func contains(list []string, want string) bool {
	for _, v := range list {
		if v == want {
			return true
		}
	}
	return false
}

// ParseSourceLine parses a single sources.list entry, also accepting the
// "ppa:user/name" shorthand and a bare "URI suite component..." triple.
func ParseSourceLine(line string) (AptSource, error) {
	line = strings.TrimSpace(line)
	if line == "" {
		return AptSource{}, fmt.Errorf("empty repository line")
	}
	// A catalog key such as "docker" or "pgdg" is accepted anywhere a
	// repository can be given.
	if e, ok := LookupCatalog(line); ok {
		src := e.Source
		if src.Name == "" {
			src.Name = e.Key
		}
		return src, nil
	}
	if strings.HasPrefix(line, "ppa:") {
		spec := strings.TrimPrefix(line, "ppa:")
		owner, name, ok := strings.Cut(spec, "/")
		if !ok || owner == "" || name == "" {
			return AptSource{}, fmt.Errorf("a PPA looks like ppa:owner/name, got %q", line)
		}
		return PPASource(owner, name), nil
	}
	if !strings.HasPrefix(line, "deb ") && !strings.HasPrefix(line, "deb-src ") {
		line = "deb " + line
	}
	got, err := parseOneLine(line, "<input>")
	if err != nil {
		return AptSource{}, err
	}
	if len(got) == 0 {
		return AptSource{}, fmt.Errorf("not a usable deb line: %q", line)
	}
	return got[0], nil
}

// PPASource builds the Launchpad repository for ppa:owner/name.
func PPASource(owner, name string) AptSource {
	return AptSource{
		Name:       "ppa-" + owner + "-" + name,
		URI:        "https://ppa.launchpadcontent.net/" + owner + "/" + name + "/ubuntu",
		Suites:     []string{UbuntuCodename},
		Components: []string{"main"},
		// A PPA is signed with its own Launchpad key, which this tool does
		// not carry; artifacts are still verified against the SHA256 in the
		// index fetched over HTTPS, so the optional gpgv step is skipped.
		Trusted: true,
	}
}

// signature identifies a source for de-duplication.
func (s AptSource) signature() string {
	return s.URI + "|" + strings.Join(s.Suites, ",") + "|" + strings.Join(s.Components, ",")
}

// AptSources merges the built-in Ubuntu archives with catalog entries named
// in the configuration, sources declared inline, and sources files.
func (c *Config) AptSources() ([]AptSource, error) {
	out := append([]AptSource{}, c.APT.Sources...)
	fromCatalog, err := SourcesFromCatalog(c.APT.Repos)
	if err != nil {
		return nil, err
	}
	out = append(out, fromCatalog...)
	for _, p := range c.APT.SourcesFiles {
		got, err := ParseSourcesFile(p)
		if err != nil {
			return nil, err
		}
		out = append(out, got...)
	}
	if c.APT.UseDefaultSources() {
		out = append(out, DefaultAptSources()...)
	}
	seen := map[string]bool{}
	deduped := out[:0]
	for _, s := range out {
		s.URI = strings.TrimRight(s.URI, "/")
		if seen[s.signature()] {
			continue
		}
		seen[s.signature()] = true
		deduped = append(deduped, s)
	}
	out = deduped
	for i := range out {
		if len(out[i].Arch) == 0 {
			out[i].Arch = c.APT.Arch
		}
		if out[i].Name == "" {
			out[i].Name = sourceName(out[i])
		}
		out[i].URI = strings.TrimRight(out[i].URI, "/")
	}
	return out, nil
}
