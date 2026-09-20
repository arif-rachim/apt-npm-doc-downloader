package apt

import (
	"strconv"
	"strings"
)

// Version is a parsed Debian version string.
type Version struct {
	Raw      string
	Epoch    int
	Upstream string
	Revision string
}

// ParseVersion splits "epoch:upstream-revision" the way dpkg does.
func ParseVersion(s string) Version {
	v := Version{Raw: s}
	rest := s
	if i := strings.Index(rest, ":"); i >= 0 {
		if n, err := strconv.Atoi(rest[:i]); err == nil {
			v.Epoch = n
			rest = rest[i+1:]
		}
	}
	if i := strings.LastIndex(rest, "-"); i >= 0 {
		v.Upstream = rest[:i]
		v.Revision = rest[i+1:]
	} else {
		v.Upstream = rest
	}
	return v
}

// CompareVersions implements dpkg's version ordering: it returns a negative
// number when a sorts before b, zero when they are equal, positive otherwise.
func CompareVersions(a, b string) int {
	va, vb := ParseVersion(a), ParseVersion(b)
	if va.Epoch != vb.Epoch {
		if va.Epoch < vb.Epoch {
			return -1
		}
		return 1
	}
	if c := verrevcmp(va.Upstream, vb.Upstream); c != 0 {
		return c
	}
	return verrevcmp(va.Revision, vb.Revision)
}

func isDigit(c byte) bool { return c >= '0' && c <= '9' }

// order mirrors dpkg's order(): '~' sorts before end-of-string, which sorts
// before letters, which sort before every other character.
func order(c byte) int {
	switch {
	case isDigit(c):
		return 0
	case (c >= 'a' && c <= 'z') || (c >= 'A' && c <= 'Z'):
		return int(c)
	case c == '~':
		return -1
	default:
		return int(c) + 256
	}
}

func verrevcmp(a, b string) int {
	i, j := 0, 0
	for i < len(a) || j < len(b) {
		firstDiff := 0
		for (i < len(a) && !isDigit(a[i])) || (j < len(b) && !isDigit(b[j])) {
			ac, bc := 0, 0
			if i < len(a) {
				ac = order(a[i])
			}
			if j < len(b) {
				bc = order(b[j])
			}
			if ac != bc {
				return ac - bc
			}
			i++
			j++
		}
		for i < len(a) && a[i] == '0' {
			i++
		}
		for j < len(b) && b[j] == '0' {
			j++
		}
		for i < len(a) && isDigit(a[i]) && j < len(b) && isDigit(b[j]) {
			if firstDiff == 0 {
				firstDiff = int(a[i]) - int(b[j])
			}
			i++
			j++
		}
		if i < len(a) && isDigit(a[i]) {
			return 1
		}
		if j < len(b) && isDigit(b[j]) {
			return -1
		}
		if firstDiff != 0 {
			return firstDiff
		}
	}
	return 0
}

// SatisfiesConstraint evaluates a dependency relation such as (>= 1.2-3).
// op uses dpkg syntax: <<, <=, =, >=, >> (and the deprecated < and >).
func SatisfiesConstraint(have, op, want string) bool {
	if op == "" || want == "" {
		return true
	}
	c := CompareVersions(have, want)
	switch op {
	case "<<":
		return c < 0
	case "<=", "<":
		return c <= 0
	case "=", "==":
		return c == 0
	case ">=", ">":
		return c >= 0
	case ">>":
		return c > 0
	default:
		return true
	}
}
