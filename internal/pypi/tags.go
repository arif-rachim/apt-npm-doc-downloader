// Package pypi mirrors Python wheels and sdists for a fixed target
// interpreter and platform.
package pypi

import (
	"fmt"
	"strconv"
	"strings"
)

// Target describes the machine the mirror is being built for.
type Target struct {
	PyMajor, PyMinor       int
	GlibcMajor, GlibcMinor int
	Arch                   string // "x86_64"
}

// ParseTarget builds a Target from config values such as "3.12" and "2.39".
func ParseTarget(python, glibc, arch string) (Target, error) {
	t := Target{Arch: arch}
	if arch == "" {
		t.Arch = "x86_64"
	}
	var err error
	if t.PyMajor, t.PyMinor, err = splitTwo(python, 3, 12); err != nil {
		return t, fmt.Errorf("python %q: %w", python, err)
	}
	if t.GlibcMajor, t.GlibcMinor, err = splitTwo(glibc, 2, 39); err != nil {
		return t, fmt.Errorf("glibc %q: %w", glibc, err)
	}
	return t, nil
}

func splitTwo(s string, defMajor, defMinor int) (int, int, error) {
	if s == "" {
		return defMajor, defMinor, nil
	}
	parts := strings.Split(s, ".")
	major, err := strconv.Atoi(parts[0])
	if err != nil {
		return 0, 0, err
	}
	minor := 0
	if len(parts) > 1 {
		if minor, err = strconv.Atoi(parts[1]); err != nil {
			return 0, 0, err
		}
	}
	return major, minor, nil
}

// PyTag is the interpreter tag of the target, e.g. "cp312".
func (t Target) PyTag() string { return fmt.Sprintf("cp%d%d", t.PyMajor, t.PyMinor) }

// Wheel is a parsed wheel filename.
type Wheel struct {
	Name    string
	Version string
	Build   string
	Py      []string
	ABI     []string
	Plat    []string
}

// ParseWheelName splits {name}-{version}(-{build})?-{py}-{abi}-{plat}.whl.
func ParseWheelName(filename string) (Wheel, bool) {
	if !strings.HasSuffix(filename, ".whl") {
		return Wheel{}, false
	}
	stem := strings.TrimSuffix(filename, ".whl")
	parts := strings.Split(stem, "-")
	var w Wheel
	switch len(parts) {
	case 5:
		w = Wheel{Name: parts[0], Version: parts[1], Py: dot(parts[2]), ABI: dot(parts[3]), Plat: dot(parts[4])}
	case 6:
		w = Wheel{Name: parts[0], Version: parts[1], Build: parts[2], Py: dot(parts[3]), ABI: dot(parts[4]), Plat: dot(parts[5])}
	default:
		return Wheel{}, false
	}
	return w, true
}

func dot(s string) []string { return strings.Split(s, ".") }

// Score ranks a wheel for the target. It returns false when the wheel cannot
// run there at all. Higher scores win; the platform tag dominates, so a
// manylinux build always beats a pure-python fallback, and a newer glibc
// baseline beats an older one.
func (t Target) Score(w Wheel) (int, bool) {
	best := -1
	for _, plat := range w.Plat {
		ps, ok := t.platScore(plat)
		if !ok {
			continue
		}
		for _, abi := range w.ABI {
			for _, py := range w.Py {
				as, ys, ok := t.abiPyScore(abi, py)
				if !ok {
					continue
				}
				if s := ps*1000 + as*100 + ys; s > best {
					best = s
				}
			}
		}
	}
	if best < 0 {
		return 0, false
	}
	return best, true
}

// platScore maps a platform tag to a preference number.
func (t Target) platScore(plat string) (int, bool) {
	switch {
	case plat == "any":
		return 0, true
	case plat == "linux_"+t.Arch:
		return 1, true
	case strings.HasPrefix(plat, "manylinux"):
		major, minor, arch, ok := parseManylinux(plat)
		if !ok || arch != t.Arch {
			return 0, false
		}
		if major > t.GlibcMajor || (major == t.GlibcMajor && minor > t.GlibcMinor) {
			return 0, false
		}
		// Prefer the highest glibc baseline the target can still run.
		return 2 + major*100 + minor, true
	default:
		// musllinux, macosx, win*, other architectures.
		return 0, false
	}
}

// parseManylinux understands both the legacy aliases and PEP 600 names.
func parseManylinux(plat string) (major, minor int, arch string, ok bool) {
	switch {
	case strings.HasPrefix(plat, "manylinux1_"):
		return 2, 5, strings.TrimPrefix(plat, "manylinux1_"), true
	case strings.HasPrefix(plat, "manylinux2010_"):
		return 2, 12, strings.TrimPrefix(plat, "manylinux2010_"), true
	case strings.HasPrefix(plat, "manylinux2014_"):
		return 2, 17, strings.TrimPrefix(plat, "manylinux2014_"), true
	case strings.HasPrefix(plat, "manylinux_"):
		rest := strings.TrimPrefix(plat, "manylinux_")
		f := strings.SplitN(rest, "_", 3)
		if len(f) != 3 {
			return 0, 0, "", false
		}
		ma, err1 := strconv.Atoi(f[0])
		mi, err2 := strconv.Atoi(f[1])
		if err1 != nil || err2 != nil {
			return 0, 0, "", false
		}
		return ma, mi, f[2], true
	}
	return 0, 0, "", false
}

// abiPyScore validates the ABI/interpreter pair and ranks it.
func (t Target) abiPyScore(abi, py string) (abiScore, pyScore int, ok bool) {
	target := t.PyTag()
	switch {
	case abi == target:
		if py != target {
			return 0, 0, false
		}
		return 3, 9, true
	case abi == "abi3":
		// Stable ABI: any cp3x build with x <= target minor works.
		if !strings.HasPrefix(py, "cp") {
			return 0, 0, false
		}
		major, minor, ok := parseCPTag(py)
		if !ok || major != t.PyMajor || minor > t.PyMinor {
			return 0, 0, false
		}
		return 2, minor, true
	case abi == "none":
		switch {
		case py == target:
			return 1, 9, true
		case py == fmt.Sprintf("py%d%d", t.PyMajor, t.PyMinor):
			return 1, 8, true
		case py == fmt.Sprintf("py%d", t.PyMajor):
			return 1, 5, true
		case strings.HasPrefix(py, "cp"):
			major, minor, ok := parseCPTag(py)
			// A cp-specific build with abi none still needs the exact
			// interpreter version.
			if ok && major == t.PyMajor && minor == t.PyMinor {
				return 1, 9, true
			}
			return 0, 0, false
		case strings.HasPrefix(py, "py"):
			major, minor, ok := parseCPTag(py)
			if ok && major == t.PyMajor && minor <= t.PyMinor {
				return 1, 4, true
			}
			return 0, 0, false
		}
	}
	return 0, 0, false
}

// parseCPTag reads "cp312" / "py39" / "py3" into major and minor numbers.
func parseCPTag(tag string) (major, minor int, ok bool) {
	digits := strings.TrimLeft(tag, "abcdefghijklmnopqrstuvwxyz")
	if digits == "" {
		return 0, 0, false
	}
	n, err := strconv.Atoi(digits)
	if err != nil {
		return 0, 0, false
	}
	if len(digits) == 1 {
		return n, 0, true
	}
	return n / int(pow10(len(digits)-1)), n % int(pow10(len(digits)-1)), true
}

func pow10(n int) int64 {
	p := int64(1)
	for i := 0; i < n; i++ {
		p *= 10
	}
	return p
}
