package apt

import (
	"fmt"
	"sort"
	"strings"
)

// Dep is one alternative inside a dependency group.
type Dep struct {
	Name    string
	Arch    string
	Op      string
	Version string
}

// ParseRelations splits a Depends-style field into groups of alternatives.
func ParseRelations(field string) [][]Dep {
	var groups [][]Dep
	for _, group := range strings.Split(field, ",") {
		group = strings.TrimSpace(group)
		if group == "" {
			continue
		}
		var alts []Dep
		for _, alt := range strings.Split(group, "|") {
			if d, ok := parseDep(alt); ok {
				alts = append(alts, d)
			}
		}
		if len(alts) > 0 {
			groups = append(groups, alts)
		}
	}
	return groups
}

func parseDep(s string) (Dep, bool) {
	s = strings.TrimSpace(s)
	if s == "" {
		return Dep{}, false
	}
	// Strip build profiles and architecture restrictions: <!nocheck> [!amd64]
	for {
		trimmed := false
		if i := strings.Index(s, "<"); i >= 0 {
			if j := strings.Index(s[i:], ">"); j >= 0 {
				s = strings.TrimSpace(s[:i] + s[i+j+1:])
				trimmed = true
			}
		}
		if i := strings.Index(s, "["); i >= 0 {
			if j := strings.Index(s[i:], "]"); j >= 0 {
				s = strings.TrimSpace(s[:i] + s[i+j+1:])
				trimmed = true
			}
		}
		if !trimmed {
			break
		}
	}
	var d Dep
	if i := strings.Index(s, "("); i >= 0 {
		rel := strings.TrimSpace(strings.Trim(s[i:], "()"))
		s = strings.TrimSpace(s[:i])
		rel = strings.TrimSpace(strings.TrimSuffix(rel, ")"))
		fields := strings.Fields(rel)
		if len(fields) == 2 {
			d.Op, d.Version = fields[0], fields[1]
		} else if len(fields) == 1 {
			d.Version = fields[0]
			d.Op = "="
		}
	}
	name := strings.TrimSpace(s)
	if name == "" {
		return Dep{}, false
	}
	if i := strings.Index(name, ":"); i >= 0 {
		d.Arch = name[i+1:]
		name = name[:i]
	}
	d.Name = name
	return d, true
}

// Resolver walks the dependency graph.
type Resolver struct {
	Index             *Index
	IncludeRecommends bool
	Exclude           map[string]bool
	// Prefer maps a dependency name to the alternative that should win when
	// the field reads "a | b".
	Prefer map[string]string

	warnings []string
}

// Result is the outcome of a closure computation.
type Result struct {
	Packages []*Package
	// Missing lists dependencies no configured repository provides.
	Missing []string
	// Warnings collects non-fatal notes (picked alternatives, skips).
	Warnings []string
}

// Closure resolves roots and every transitive Depends/Pre-Depends. Roots may
// be written as "name", "name=version" or "name>=version".
func (r *Resolver) Closure(roots []string) (*Result, error) {
	selected := map[string]*Package{}
	missing := map[string]bool{}
	var queue []Dep

	for _, root := range roots {
		d, err := parseRoot(root)
		if err != nil {
			return nil, err
		}
		p := r.Index.Best(d.Name, d.Op, d.Version)
		if p == nil {
			return nil, fmt.Errorf("requested package %q not found in any configured repository", root)
		}
		queue = append(queue, d)
	}

	for len(queue) > 0 {
		d := queue[0]
		queue = queue[1:]
		if r.Exclude[d.Name] {
			continue
		}
		if p, ok := selected[d.Name]; ok {
			if SatisfiesConstraint(p.Version, d.Op, d.Version) {
				continue
			}
			// A stricter constraint showed up later; try to upgrade.
			better := r.Index.Best(d.Name, d.Op, d.Version)
			if better == nil {
				r.warn("conflicting constraint for %s (%s %s), keeping %s", d.Name, d.Op, d.Version, p.Version)
				continue
			}
			selected[d.Name] = better
			queue = append(queue, r.expand(better)...)
			continue
		}
		p := r.Index.Best(d.Name, d.Op, d.Version)
		if p == nil {
			missing[relationString(d)] = true
			continue
		}
		selected[d.Name] = p
		if p.Name != d.Name {
			// Resolved through Provides.
			selected[p.Name] = p
		}
		queue = append(queue, r.expand(p)...)
	}

	out := make([]*Package, 0, len(selected))
	seen := map[string]bool{}
	for _, p := range selected {
		key := p.Name + "_" + p.Version + "_" + p.Architecture
		if seen[key] {
			continue
		}
		seen[key] = true
		out = append(out, p)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })

	var miss []string
	for m := range missing {
		miss = append(miss, m)
	}
	sort.Strings(miss)
	return &Result{Packages: out, Missing: miss, Warnings: r.warnings}, nil
}

// expand turns a package's dependency fields into queue entries, resolving
// alternatives.
func (r *Resolver) expand(p *Package) []Dep {
	var out []Dep
	for _, groups := range [][][]Dep{
		ParseRelations(p.PreDepends),
		ParseRelations(p.Depends),
	} {
		out = append(out, r.pick(groups)...)
	}
	if r.IncludeRecommends {
		out = append(out, r.pick(ParseRelations(p.Recommends))...)
	}
	return out
}

// pick chooses one alternative per group: an explicit preference first, then
// the first alternative any repository can satisfy.
func (r *Resolver) pick(groups [][]Dep) []Dep {
	var out []Dep
	for _, alts := range groups {
		if len(alts) == 1 {
			out = append(out, alts[0])
			continue
		}
		chosen := -1
		if want, ok := r.Prefer[alts[0].Name]; ok {
			for i, a := range alts {
				if a.Name == want {
					chosen = i
					break
				}
			}
		}
		if chosen < 0 {
			for i, a := range alts {
				if r.Exclude[a.Name] {
					continue
				}
				if r.Index.Best(a.Name, a.Op, a.Version) != nil {
					chosen = i
					break
				}
			}
		}
		if chosen < 0 {
			out = append(out, alts[0])
			continue
		}
		if chosen != 0 {
			r.warn("alternative %q resolved to %q", alts[0].Name, alts[chosen].Name)
		}
		out = append(out, alts[chosen])
	}
	return out
}

func (r *Resolver) warn(format string, args ...any) {
	r.warnings = append(r.warnings, fmt.Sprintf(format, args...))
}

func relationString(d Dep) string {
	if d.Op == "" {
		return d.Name
	}
	return fmt.Sprintf("%s (%s %s)", d.Name, d.Op, d.Version)
}

func parseRoot(s string) (Dep, error) {
	s = strings.TrimSpace(s)
	for _, op := range []string{">=", "<=", ">>", "<<", "="} {
		if i := strings.Index(s, op); i > 0 {
			return Dep{Name: strings.TrimSpace(s[:i]), Op: op, Version: strings.TrimSpace(s[i+len(op):])}, nil
		}
	}
	if s == "" {
		return Dep{}, fmt.Errorf("empty package name")
	}
	return Dep{Name: s}, nil
}
