package pypi

import (
	"math"
	"regexp"
	"strconv"
	"strings"
)

// Ver is a pragmatic PEP 440 version: enough to order releases and evaluate
// the specifiers that appear in requires-python and pinned requirements.
type Ver struct {
	Epoch   int
	Release []int
	PreKind int // -1 none, 0 dev-only, 1 a, 2 b, 3 rc, 4 final
	PreNum  int
	Post    int // -1 when absent
	Dev     int // math.MaxInt when absent
}

var verRE = regexp.MustCompile(`^(?:(\d+)!)?(\d+(?:\.\d+)*)((?:[.\-_]?(?:a|b|c|rc|alpha|beta|pre|preview)[.\-_]?\d*)?)((?:[.\-_]?(?:post|rev|r)[.\-_]?\d*|-\d+)?)((?:[.\-_]?dev[.\-_]?\d*)?)(?:\+.*)?$`)

// ParseVersion reads a PEP 440 version string. Unparseable input yields a
// zero Ver, which sorts before everything else.
func ParseVersion(s string) Ver {
	v := Ver{PreKind: 4, Post: -1, Dev: math.MaxInt}
	s = strings.TrimSpace(strings.ToLower(s))
	s = strings.TrimPrefix(s, "v")
	m := verRE.FindStringSubmatch(s)
	if m == nil {
		return Ver{PreKind: 4, Post: -1, Dev: math.MaxInt}
	}
	if m[1] != "" {
		v.Epoch, _ = strconv.Atoi(m[1])
	}
	for _, p := range strings.Split(m[2], ".") {
		n, _ := strconv.Atoi(p)
		v.Release = append(v.Release, n)
	}
	if m[3] != "" {
		kind, num := splitLabel(m[3], []string{"alpha", "beta", "preview", "pre", "rc", "a", "b", "c"})
		switch kind {
		case "a", "alpha":
			v.PreKind = 1
		case "b", "beta":
			v.PreKind = 2
		default:
			v.PreKind = 3
		}
		v.PreNum = num
	}
	if m[4] != "" {
		_, num := splitLabel(m[4], []string{"post", "rev", "r"})
		v.Post = num
	}
	if m[5] != "" {
		_, num := splitLabel(m[5], []string{"dev"})
		v.Dev = num
		if m[3] == "" && m[4] == "" {
			v.PreKind = 0
		}
	}
	return v
}

func splitLabel(s string, labels []string) (string, int) {
	s = strings.Trim(s, ".-_")
	for _, l := range labels {
		if strings.HasPrefix(s, l) {
			num, _ := strconv.Atoi(strings.Trim(strings.TrimPrefix(s, l), ".-_"))
			return l, num
		}
	}
	num, _ := strconv.Atoi(strings.Trim(s, ".-_"))
	return "", num
}

// CompareVersions orders two PEP 440 versions.
func CompareVersions(a, b string) int {
	return ParseVersion(a).Compare(ParseVersion(b))
}

// Compare returns -1, 0 or 1.
func (v Ver) Compare(o Ver) int {
	if c := cmpInt(v.Epoch, o.Epoch); c != 0 {
		return c
	}
	n := len(v.Release)
	if len(o.Release) > n {
		n = len(o.Release)
	}
	for i := 0; i < n; i++ {
		if c := cmpInt(at(v.Release, i), at(o.Release, i)); c != 0 {
			return c
		}
	}
	if c := cmpInt(v.PreKind, o.PreKind); c != 0 {
		return c
	}
	if c := cmpInt(v.PreNum, o.PreNum); c != 0 {
		return c
	}
	if c := cmpInt(v.Post, o.Post); c != 0 {
		return c
	}
	return cmpInt(v.Dev, o.Dev)
}

func at(s []int, i int) int {
	if i < len(s) {
		return s[i]
	}
	return 0
}

func cmpInt(a, b int) int {
	switch {
	case a < b:
		return -1
	case a > b:
		return 1
	}
	return 0
}

// RequiresPythonOK evaluates a requires-python specifier against the target
// interpreter. An empty specifier always passes.
func RequiresPythonOK(spec string, t Target) bool {
	if strings.TrimSpace(spec) == "" {
		return true
	}
	have := strconv.Itoa(t.PyMajor) + "." + strconv.Itoa(t.PyMinor)
	return MatchSpecifier(spec, have)
}

// MatchSpecifier evaluates a comma separated PEP 440 specifier set.
func MatchSpecifier(spec, version string) bool {
	for _, clause := range strings.Split(spec, ",") {
		clause = strings.TrimSpace(clause)
		if clause == "" {
			continue
		}
		if !matchOne(clause, version) {
			return false
		}
	}
	return true
}

func matchOne(clause, version string) bool {
	ops := []string{"===", "~=", "==", "!=", ">=", "<=", ">", "<"}
	op := ""
	for _, o := range ops {
		if strings.HasPrefix(clause, o) {
			op = o
			break
		}
	}
	if op == "" {
		return true
	}
	want := strings.TrimSpace(strings.TrimPrefix(clause, op))
	v := ParseVersion(version)

	if strings.HasSuffix(want, ".*") {
		prefix := ParseVersion(strings.TrimSuffix(want, ".*"))
		eq := prefixEqual(v.Release, prefix.Release)
		switch op {
		case "==":
			return eq
		case "!=":
			return !eq
		}
		want = strings.TrimSuffix(want, ".*")
	}

	w := ParseVersion(want)
	c := v.Compare(w)
	switch op {
	case "==", "===":
		return c == 0
	case "!=":
		return c != 0
	case ">=":
		return c >= 0
	case "<=":
		return c <= 0
	case ">":
		return c > 0
	case "<":
		return c < 0
	case "~=":
		// Compatible release: >= want, and same prefix minus the last
		// component.
		if c < 0 {
			return false
		}
		if len(w.Release) < 2 {
			return true
		}
		return prefixEqual(v.Release, w.Release[:len(w.Release)-1])
	}
	return true
}

func prefixEqual(have, prefix []int) bool {
	for i, p := range prefix {
		if at(have, i) != p {
			return false
		}
	}
	return true
}
